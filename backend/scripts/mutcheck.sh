#!/usr/bin/env bash
# 删保护验证：临时改坏一处代码，确认有测试因此失败，然后还原。
#
#   backend/scripts/mutcheck.sh [--run <regex>] <文件> <原文> <替换> [go test 参数，默认 ./...]
#   backend/scripts/mutcheck.sh --restore
#
# 备份固定保存在仓库根目录 .dev/mutcheck-backup/（原始字节和绝对路径），仅一个槽。
# 中断后先用 --restore 还原；完整槽拒绝新的验证，未发布 original 的槽可安全清理。
# <文件> 相对当前目录；go test 参数里的包路径相对 backend/。
# 原文必须在文件中恰好出现一次；替换后要测的包必须能构建；测试选择只通过 --run 传入。
# 改的是 internal/db/queries 下的查询时，改坏后重新运行 sqlc 生成代码，结束时连生成的代码一起还原。
# 结束时无论成功失败都还原文件。
# 未设置 GOCACHE（单独运行或 --restore）时使用 .dev/mutcheck-gocache-standalone.* 临时缓存，
# 退出时删除；由 mutchecks.py 启动时沿用其运行缓存。go test 均带 -trimpath。
# 退出码：0 有测试失败（被发现）；1 测试全部通过（没被发现）；
#         2 用法、匹配或构建错误；3 测试失败但没有 --- FAIL 行（超时、panic 等，需人工查看）；
#         4 PostgreSQL 容器环境失败（重试一次后仍失败，不计为捕获）。
set -euo pipefail

backend=$(cd "$(dirname "$0")/.." && pwd -P)
slot="$backend/../.dev/mutcheck-backup"
discard_incomplete() {
  if [ -d "$slot" ] && [ ! -f "$slot/original" ]; then
    rm -f "$slot/path" "$slot/original.tmp"
    rmdir "$slot"
    echo "discarded incomplete backup slot (target never mutated)"
    return 0
  fi
  return 1
}
restore() {
  if [ ! -d "$slot" ]; then
    echo "nothing to restore"
    return 0
  fi
  if discard_incomplete; then return 0; fi
  if [ ! -f "$slot/path" ]; then
    echo "mutcheck: incomplete backup slot: $slot; inspect before removing" >&2
    return 2
  fi
  local target
  target=$(cat "$slot/path")
  cp "$slot/original" "$target" || return 2
  case "$target" in "$backend"/internal/db/queries/*)
    (cd "$backend" && go tool sqlc generate) || return 2 ;;
  esac
  rm "$slot/original" "$slot/path"
  rmdir "$slot"
  echo "restored: $target"
}
# 单独运行时隔离 Go 构建缓存；退出（含信号）时删除，不碰用户的默认缓存。
owned_cache=
cleanup_cache() {
  if [ -n "$owned_cache" ]; then
    rm -rf "$owned_cache"
  fi
}
setup_cache() {
  if [ -z "${GOCACHE:-}" ]; then
    mkdir -p "$backend/../.dev"
    owned_cache=$(mktemp -d "$backend/../.dev/mutcheck-gocache-standalone.XXXXXX")
    export GOCACHE=$owned_cache
  fi
}
if [ "${1:-}" = --restore ] && [ $# -eq 1 ]; then
  setup_cache
  trap cleanup_cache EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  restore
  exit $?
fi
setup_cache
trap cleanup_cache EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
if [ -d "$slot" ] && ! discard_incomplete; then
  echo "mutcheck: backup occupied for $(cat "$slot/path" 2>/dev/null || echo unknown); restore with: $backend/scripts/mutcheck.sh --restore" >&2
  exit 2
fi
test_selection=()
if [ "${1:-}" = --run ]; then
  [ -n "${2:-}" ] || { echo "mutcheck: --run requires a nonempty regex" >&2; exit 2; }
  test_selection=(-run "$2")
  shift 2
fi
[ $# -ge 3 ] || { echo "usage: backend/scripts/mutcheck.sh [--run <regex>] <file> <old> <new> [go test args] | --restore" >&2; exit 2; }
file=$(realpath "$1") old=$2 new=$3; shift 3
[ -f "$file" ] || { echo "mutcheck: no such file: $file" >&2; exit 2; }
[ $# -gt 0 ] || set -- ./...
cd "$backend"
mkdir -p "$(dirname "$slot")"
if ! mkdir "$slot" 2>/dev/null; then
  echo "mutcheck: backup occupied for $(cat "$slot/path" 2>/dev/null || echo unknown); restore with: $backend/scripts/mutcheck.sh --restore" >&2
  exit 2
fi
printf '%s\n' "$file" > "$slot/path"
cp "$file" "$slot/original.tmp"
mv "$slot/original.tmp" "$slot/original"
regen=false
case "$file" in "$PWD"/internal/db/queries/*) regen=true ;; esac
cleanup() {
  local status=$?
  restore || status=$?
  cleanup_cache
  return "$status"
}
trap cleanup EXIT

python3 - "$file" "$old" "$new" <<'PY' || exit 2
import sys
path, old, new = sys.argv[1:]
with open(path, newline="") as f:
    src = f.read()
n = src.count(old)
if n != 1:
    sys.exit(f"mutcheck: original text occurs {n} times in {path}; make it unique")
with open(path, "w", newline="") as f:
    f.write(src.replace(old, new))
PY

if $regen && ! go tool sqlc generate; then
  echo "mutcheck: sqlc generate failed on the mutated query" >&2
  exit 2
fi

# 只编译、不运行测试，确认改坏后的代码仍能构建（例如查询的参数没变）。
if ! build=$(go test -count=1 -trimpath -run '^$' "$@" 2>&1); then
  echo "mutcheck: mutated code does not build:" >&2
  head -5 <<<"$build" >&2
  exit 2
fi

for attempt in 1 2; do
  status=0
  out=$(go test -count=1 -trimpath "${test_selection[@]}" "$@" 2>&1) || status=$?
  if [[ "$out" == *"启动 PostgreSQL 测试容器失败"* ]]; then
    echo "mutcheck: environment failure (attempt $attempt/2):" >&2
    if [ "$attempt" -eq 1 ]; then
      echo "mutcheck: retrying test run once" >&2
      continue
    fi
    echo "$out" >&2
    echo "not run (environment): PostgreSQL test container failed after retry" >&2
    exit 4
  fi
  if [ "$status" -eq 0 ]; then
    echo "NOT caught: all tests passed with the mutation"
    exit 1
  fi
  break
done
fails=$(grep -E '^--- FAIL' <<<"$out" | sed 's/ (.*//' || true)
if [ -n "$fails" ]; then
  echo "caught by:"
  echo "$fails"
  exit 0
fi
echo "mutcheck: tests failed without a --- FAIL line (timeout or panic?); output tail:" >&2
tail -15 <<<"$out" >&2
exit 3
