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
# 启动前拒绝接管其他进程占用的端口；健康检查同时确认记录的服务进程仍在运行。
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
    check_wait_process "$what" || return 1
    if "$@" >/dev/null 2>&1; then
      check_wait_process "$what"
      return $?
    fi
    sleep 1
  done
  check_wait_process "$what" || return 1
  log "timed out waiting for $what"
  return 1
}

check_wait_process() {
  case "$1" in
    backend|admin)
      if ! proc_running "$1"; then
        log "$1 exited while waiting for readiness; last 20 log lines:"
        tail -n 20 "$STATE/$1.log" >&2
        return 1
      fi
      ;;
  esac
}

# Nuxt 的监听者可能是子进程；只接受 pid 文件所记录的进程组。
check_port() { # 名字 端口
  local name=$1 port=$2 listeners pids pid pgid expected=""
  if command -v ss >/dev/null; then
    if ! listeners=$(ss -H -ltnp "sport = :$port"); then
      log "$name: cannot check port $port: ss failed"
      return 1
    fi
    [ -n "$listeners" ] || return 0
    pids=$(printf '%s\n' "$listeners" | grep -oE 'pid=[0-9]+' | cut -d= -f2 || true)
    # 看不到占用者的行不能当作属于本服务（例如其他用户的监听进程）。
    if printf '%s\n' "$listeners" | grep -qv 'pid='; then pids=""; fi
  else
    pids=""
    # Linux（含 WSL）：没有 ss 时只判断端口占用，不猜测监听者归属。
    if ! awk -v port="$(printf '%04X' "$port")" \
      '$4 == "0A" && substr($2, length($2) - 4) == ":" port { found=1 }
       END { exit !found }' /proc/net/tcp /proc/net/tcp6; then
      return 0
    fi
    listeners="port $port is occupied (owner unavailable)"
  fi
  if proc_running "$name"; then
    if ! expected=$(cat "$STATE/$name.pid"); then
      log "$name: cannot check port $port: cannot read pid file"
      return 1
    fi
  fi
  if [ -n "$expected" ] && [ -n "$pids" ]; then
    for pid in $pids; do
      pgid=$(ps -o pgid= -p "$pid" | tr -d ' ' || true)
      [ "$pgid" = "$expected" ] || break
    done
    if [ "$pgid" = "$expected" ]; then return 0; fi
  fi
  log "$name port $port is occupied; stop the occupying process first:"
  log "$listeners"
  for pid in $pids; do
    ps -ww -p "$pid" -o pid=,args= >&2 || true
  done
  return 1
}

start_db() {
  if db_running; then return; fi
  if docker inspect "$DB_CONTAINER" >/dev/null 2>&1; then
    docker start "$DB_CONTAINER" >/dev/null
  else
    docker run -d --name "$DB_CONTAINER" -e POSTGRES_PASSWORD=dev -p "127.0.0.1:$DB_PORT:5432" postgres:18-alpine >/dev/null
  fi
  wait_for postgres docker exec "$DB_CONTAINER" pg_isready -U postgres
  # pg_isready 在初始化阶段的临时实例上也会成功；等到能执行查询为止。
  wait_for postgres docker exec "$DB_CONTAINER" psql -U postgres -c 'SELECT 1'
}

# 用进程组启动，down 时整组停止（npx nuxt 会派生子进程）。
start_proc() { # 名字 目录 端口 命令...
  local name=$1 dir=$2 port=$3; shift 3
  check_port "$name" "$port"
  if proc_running "$name"; then return; fi
  rm -f "$STATE/$name.pid"
  # setsid 可能先 fork，$! 拿到的不是真正的进程；由新会话里的 shell 自己写 pid 再 exec。
  # 整个后台列表都重定向，包含中间 shell，避免持有调用者的管道而阻止 EOF。
  ( (cd "$dir" && setsid bash -c 'echo $$ >"$0"; exec "$@"' "$STATE/$name.pid" "$@") \
    </dev/null >"$STATE/$name.log" 2>&1 &)
  wait_for "$name pid file" test -s "$STATE/$name.pid"
}

proc_running() {
  local pidfile="$STATE/$1.pid" stat
  [ -s "$pidfile" ] || return 1
  # 查询进程表不需要发信号的权限；僵尸进程已经退出，不能算健康。
  stat=$(ps -o stat= -p "$(cat "$pidfile")") || return 1
  [[ -n "$stat" && "$stat" != *Z* ]]
}

# 组长退出不代表子进程退出；僵尸进程不再算作运行中的组员。
group_running() {
  ps -eo pgid=,stat= | awk -v pgid="$1" \
    '$1 == pgid && $2 !~ /^Z/ { found=1 } END { exit !found }'
}

# 停止整个进程组，并等它真正退出，避免紧接着的 up 撞上还没释放的端口。
stop_proc() {
  local pidfile="$STATE/$1.pid" pid
  if [ -f "$pidfile" ]; then
    pid=$(cat "$pidfile")
    kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
    for _ in $(seq 1 20); do
      group_running "$pid" || break
      sleep 0.5
    done
    if group_running "$pid"; then
      kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
    fi
  fi
  rm -f "$pidfile"
}

up() {
  local port_status=0
  check_port backend "${BACKEND_ADDR##*:}" || port_status=1
  check_port admin "$ADMIN_PORT" || port_status=1
  [ "$port_status" = "0" ] || return 1
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
    start_proc backend "$ROOT/backend" "${BACKEND_ADDR##*:}" "$STATE/salon" serve
  wait_for backend curl --max-time 2 -sf "http://$BACKEND_ADDR/healthz"
  if [ ! -d "$ROOT/admin/node_modules" ]; then (cd "$ROOT/admin" && npm install); fi
  start_proc admin "$ROOT/admin" "$ADMIN_PORT" npx nuxt dev --host localhost --port "$ADMIN_PORT"
  wait_for admin curl --max-time 2 -sf "http://localhost:$ADMIN_PORT/"
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
