#!/usr/bin/env bash
# 删保护验证：临时改坏一处代码，确认有测试因此失败，然后还原。
#
#   backend/scripts/mutcheck.sh <文件> <原文> <替换> [go test 参数，默认 ./...]
#
# <文件> 相对当前目录；go test 参数里的包路径相对 backend/。
# 原文必须在文件中恰好出现一次；替换后要测的包必须能构建（参数里不要再传 -run）。
# 结束时无论成功失败都还原文件。
# 退出码：0 有测试失败（被发现）；1 测试全部通过（没被发现）；
#         2 用法、匹配或构建错误；3 测试失败但没有 --- FAIL 行（超时、panic 等，需人工查看）。
set -euo pipefail

[ $# -ge 3 ] || { echo "usage: backend/scripts/mutcheck.sh <file> <old> <new> [go test args]" >&2; exit 2; }
file=$(realpath "$1") old=$2 new=$3; shift 3
[ -f "$file" ] || { echo "mutcheck: no such file: $1" >&2; exit 2; }
[ $# -gt 0 ] || set -- ./...
cd "$(dirname "$0")/.."

backup=$(mktemp)
cp "$file" "$backup"
trap 'cp "$backup" "$file"; rm -f "$backup"' EXIT

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

# 只编译、不运行测试，确认改坏后的代码仍能构建。对 SQL 等嵌入文件同样适用。
if ! build=$(go test -count=1 -run '^$' "$@" 2>&1); then
  echo "mutcheck: mutated code does not build:" >&2
  head -5 <<<"$build" >&2
  exit 2
fi

if out=$(go test -count=1 "$@" 2>&1); then
  echo "NOT caught: all tests passed with the mutation"
  exit 1
fi
fails=$(grep -E '^--- FAIL' <<<"$out" | sed 's/ (.*//' || true)
if [ -n "$fails" ]; then
  echo "caught by:"
  echo "$fails"
  exit 0
fi
echo "mutcheck: tests failed without a --- FAIL line (timeout or panic?); output tail:" >&2
tail -15 <<<"$out" >&2
exit 3
