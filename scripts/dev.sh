#!/usr/bin/env bash
# 本地开发环境：PostgreSQL 容器、后端、管理后台。
#
#   scripts/dev.sh up      启动全部（数据库已存在则沿用其数据）；首次启动时开店
#   scripts/dev.sh down    按 pid 停止后端和管理后台，停止数据库容器
#   scripts/dev.sh reset   删除数据库后重新 up（全新数据）
#   scripts/dev.sh status  显示各部分是否在运行
#
# 环境变量：DEV_DB_PORT、DEV_BACKEND_ADDR、DEV_ADMIN_PORT、DEV_WAIT_SECONDS（每一步最多等多少秒，默认 60）。
#
# 进程的 pid 和日志在 .dev/ 下。停止进程只按 pid 文件里的进程组，不按名字匹配。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STATE="$ROOT/.dev"
DB_CONTAINER=salon-dev-db
DB_PORT="${DEV_DB_PORT:-55432}"
BACKEND_ADDR="${DEV_BACKEND_ADDR:-127.0.0.1:8080}"
ADMIN_PORT="${DEV_ADMIN_PORT:-3000}"
OWNER_ACCOUNT=boss
OWNER_PASSWORD=owner-password

export DATABASE_URL="postgres://postgres:dev@127.0.0.1:$DB_PORT/postgres?sslmode=disable"
# Go 装在用户目录时（见 backend/README），当前 shell 可能还没有它。
if ! command -v go >/dev/null && [ -x "$HOME/.local/go/bin/go" ]; then
  export PATH="$HOME/.local/go/bin:$PATH"
fi
WAIT_SECONDS="${DEV_WAIT_SECONDS:-60}"

mkdir -p "$STATE"

log() { printf '%s\n' "$*" >&2; }

db_running() { [ "$(docker inspect -f '{{.State.Running}}' "$DB_CONTAINER" 2>/dev/null)" = "true" ]; }

wait_for() { # 描述 命令...
  local what=$1; shift
  for _ in $(seq 1 "$WAIT_SECONDS"); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  log "timed out waiting for $what"
  return 1
}

start_db() {
  if db_running; then return; fi
  if docker inspect "$DB_CONTAINER" >/dev/null 2>&1; then
    docker start "$DB_CONTAINER" >/dev/null
  else
    docker run -d --name "$DB_CONTAINER" -e POSTGRES_PASSWORD=dev -p "$DB_PORT:5432" postgres:18-alpine >/dev/null
  fi
  wait_for postgres docker exec "$DB_CONTAINER" pg_isready -U postgres
  # pg_isready 在初始化阶段的临时实例上也会成功；等到能执行查询为止。
  wait_for postgres docker exec "$DB_CONTAINER" psql -U postgres -c 'SELECT 1'
}

# 用进程组启动，down 时整组停止（npx nuxt 会派生子进程）。
start_proc() { # 名字 目录 命令...
  local name=$1 dir=$2; shift 2
  if proc_running "$name"; then return; fi
  rm -f "$STATE/$name.pid"
  # setsid 可能先 fork，$! 拿到的不是真正的进程；由新会话里的 shell 自己写 pid 再 exec。
  # 标准输入接 /dev/null，否则调用者（例如代理的 shell 工具）会一直等这个后台进程。
  (cd "$dir" && setsid bash -c 'echo $$ >"$0"; exec "$@"' "$STATE/$name.pid" "$@" \
    </dev/null >"$STATE/$name.log" 2>&1 &)
  wait_for "$name pid file" test -s "$STATE/$name.pid"
}

proc_running() {
  local pidfile="$STATE/$1.pid"
  [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null
}

# 停止整个进程组，并等它真正退出，避免紧接着的 up 撞上还没释放的端口。
stop_proc() {
  local pidfile="$STATE/$1.pid" pid
  if proc_running "$1"; then
    pid=$(cat "$pidfile")
    kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
  rm -f "$pidfile"
}

up() {
  start_db
  (cd "$ROOT/backend" && go build -o "$STATE/salon" ./cmd/salon)
  "$STATE/salon" migrate up
  local tenants
  tenants=$(docker exec "$DB_CONTAINER" psql -U postgres -tAc 'SELECT count(*) FROM tenants')
  if [ "$tenants" = "0" ]; then
    BOOTSTRAP_PASSWORD=$OWNER_PASSWORD "$STATE/salon" bootstrap -tenant 本店 -store 本店 \
      -owner-name 王老板 -owner-number 001 -owner-account "$OWNER_ACCOUNT" >/dev/null
  fi
  JWT_SECRET=dev-secret-dev-secret-dev-secret-0123 HTTP_ADDR=$BACKEND_ADDR \
    start_proc backend "$ROOT/backend" "$STATE/salon" serve
  wait_for backend curl -sf "http://$BACKEND_ADDR/healthz"
  if [ ! -d "$ROOT/admin/node_modules" ]; then (cd "$ROOT/admin" && npm install); fi
  start_proc admin "$ROOT/admin" npx nuxt dev --host localhost --port "$ADMIN_PORT"
  wait_for admin curl -sf "http://localhost:$ADMIN_PORT/"
  # e2e 测试从这里读取地址和老板账号（admin/e2e/env.ts）。
  printf '{"baseURL":"http://localhost:%s","owner":{"account":"%s","password":"%s"}}\n' \
    "$ADMIN_PORT" "$OWNER_ACCOUNT" "$OWNER_PASSWORD" >"$STATE/e2e.json"
  log "admin:   http://localhost:$ADMIN_PORT  (owner: $OWNER_ACCOUNT / $OWNER_PASSWORD)"
  log "backend: http://$BACKEND_ADDR  logs: $STATE/*.log"
}

down() {
  stop_proc admin
  stop_proc backend
  if db_running; then docker stop "$DB_CONTAINER" >/dev/null; fi
  log "stopped"
}

reset() {
  down
  docker rm -f "$DB_CONTAINER" >/dev/null 2>&1 || true
  up
}

status() {
  for p in backend admin; do
    if proc_running $p; then echo "$p: running (pid $(cat "$STATE/$p.pid"))"; else echo "$p: stopped"; fi
  done
  if db_running; then echo "db: running ($DB_CONTAINER, port $DB_PORT)"; else echo "db: stopped"; fi
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  reset) reset ;;
  status) status ;;
  *) log "usage: scripts/dev.sh up|down|reset|status"; exit 2 ;;
esac
