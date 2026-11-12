#!/usr/bin/env bash
# scripts/dev/run-svc.sh — host 侧并行跑 proxy + api + runner
# 应用层（logx）自管落盘到 logs/{service}.log（lumberjack 轮转），shell 不再重定向。
# Ctrl-C 全部关闭

set -euo pipefail
cd "$(dirname "$0")/../.."

mkdir -p logs

# load .env（唯一真值文件：host 脚本 source + compose 插值共用）
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

# host 侧地址（与 compose.yaml 的宿主端口映射一致：pg 55432、redis 56379、沙盒 58591）
export LIUSHA_POSTGRES_DSN="${LIUSHA_POSTGRES_DSN:-postgres://liusha:${POSTGRES_PASSWORD:?POSTGRES_PASSWORD 未设置（.env 必填）}@localhost:${POSTGRES_HOST_PORT:-55432}/liusha?sslmode=disable}"
export LIUSHA_REDIS_ADDR="${LIUSHA_REDIS_ADDR:-localhost:${REDIS_HOST_PORT:-56379}}"
# redis 必带密码（compose 起的 redis 已 requirepass；.env 的 REDIS_PASSWORD 必填）
export LIUSHA_REDIS_PASSWORD="${REDIS_PASSWORD:?REDIS_PASSWORD 未设置（.env 必填）}"
# 沙盒在容器里（up.sh 起）：runner 经宿主端口调用它；工作区与容器内同一绝对路径（compose 同路径挂载）。
export LIUSHA_SANDBOX_URL="${LIUSHA_SANDBOX_URL:-http://127.0.0.1:${SANDBOX_HOST_PORT:-58591}}"
export LIUSHA_SANDBOX_TOKEN="${LIUSHA_SANDBOX_TOKEN:?LIUSHA_SANDBOX_TOKEN 未设置（.env 必填）}"
export LIUSHA_SANDBOX_WORKSPACE_DIR="${LIUSHA_SANDBOX_WORKSPACE_DIR:-${HOST_DATA_DIR:-$HOME/data/liusha}/workspace}"
# 初始管理员：账号表为空时 api 启动期用此密码建 admin（仅首启生效，之后改密走前端）。
# dev 用固定值方便记忆；e2e 同款凭据。
export LIUSHA_ADMIN_INITIAL_PASSWORD="${LIUSHA_ADMIN_INITIAL_PASSWORD:-changeme-admin-pass}"
export LIUSHA_ENV="${LIUSHA_ENV:-development}"
export LIUSHA_LOG_LEVEL="${LIUSHA_LOG_LEVEL:-info}"
export LIUSHA_LOG_DIR="${LIUSHA_LOG_DIR:-logs}"
# 多进程并行跑：stdout 易刷屏，dev 默认只走 file，需要时 tail -F logs/*.log
export LIUSHA_LOG_TO_STDOUT="${LIUSHA_LOG_TO_STDOUT:-false}"
export LIUSHA_LOG_TO_FILE="${LIUSHA_LOG_TO_FILE:-true}"

# 其余参数取内置默认配置（api :8580、proxy :8588、runner :8590）；要改用 LIUSHA_<键> 环境变量
# 或 LIUSHA_CONFIG 指向的覆盖文件。LLM 接入在前端「LLM 配置」页维护：未接入时服务照常启动，
# 任务下发会被拒绝并提示到前端配置。

echo "===== 编译 liusha ====="
go build -o bin/liusha ./cmd/liusha

echo "===== 启动 3 个 host 服务 ====="
echo "  postgres:      ${LIUSHA_POSTGRES_DSN%%@*}@…"
echo "  redis:         $LIUSHA_REDIS_ADDR"
echo "  sandbox:       $LIUSHA_SANDBOX_URL"
echo ""

# 预清旧 dev 进程：避免端口被旧 nohup/run-svc 进程占着导致新启动 fatal "address
# already in use"，进而出现"半新半旧"的混跑栈（曾踩坑：proxy/api 留旧版，
# runner 用新版，调试极难）。
#
# 按端口找 PID 精确 kill（三个组件是同一个程序的不同子命令，按命令行匹配会误伤）。
# 先 SIGTERM 再 SIGKILL。
for port in 8588 8580 8590; do
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1) || true
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
  fi
done
sleep 1
for port in 8588 8580 8590; do
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1) || true
  if [ -n "$pid" ]; then
    kill -9 "$pid" 2>/dev/null || true
  fi
done
sleep 1

# 端口预检：本脚本管理的 3 端口任一仍被占 → 立即报错退出。
PORT_BUSY=()
for p in 8588 8580 8590; do
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    PORT_BUSY+=("$p")
  fi
done
if [ "${#PORT_BUSY[@]}" -gt 0 ]; then
  echo "✗ 端口仍被占：${PORT_BUSY[*]}"
  echo "  → 先手动清：lsof -nP -iTCP:${PORT_BUSY[0]} -sTCP:LISTEN"
  exit 1
fi

# 启动顺序：proxy → api → runner
# 应用层 logx 直接写 logs/{service}.log；shell 这里 stderr 兜底捕获 panic 前的早期输出
echo "[1/3] proxy on :8588 (mitm)"
bin/liusha proxy 2>logs/proxy.stderr &
PROXY_PID=$!

echo "[2/3] api on :8580"
bin/liusha api 2>logs/api.stderr &
API_PID=$!

echo "[3/3] runner on :8590"
bin/liusha runner 2>logs/runner.stderr &
WORKER_PID=$!

# 等服务起来
sleep 3

# Ctrl-C 优雅关
cleanup() {
  echo ""
  echo "===== 关闭服务 ====="
  for pid in "$WORKER_PID" "$API_PID" "$PROXY_PID"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  wait 2>/dev/null || true
  echo "✓ 全部关闭"
}
trap cleanup INT TERM

echo ""
echo "✓ 三服务在跑（pids: proxy=${PROXY_PID} api=${API_PID} runner=${WORKER_PID}）"
echo ""
echo "📊 前端：web/（本仓）→ make run-web（/api 已代理到本 api）"
echo "  （登录：admin / LIUSHA_ADMIN_INITIAL_PASSWORD，仅账号表为空时初始化）"
echo ""
echo "  日志合并 tail（Ctrl-C 关闭服务+退出 tail）："
echo ""

# logx 自管落盘：等待文件出现后再 tail（避免 tail 启动时 lumberjack 还没创建文件）
for f in logs/proxy.log logs/api.log logs/runner.log; do
  for _ in $(seq 1 20); do
    [ -f "$f" ] && break
    sleep 0.5
  done
done

# 交互 TTY：tail -F 三个日志（Ctrl-C 触发上方 cleanup 优雅关停）。
# 非交互（nohup/CI，如 e2e.sh 拉起）：tail 会把脚本钉死成僵尸包装进程，
# 且多轮累积的 tail 向同一 stdout 文件灌重复流——直接退出，服务已后台运行。
if [ -t 1 ]; then
  tail -F logs/proxy.log logs/api.log logs/runner.log
else
  echo "✓ 非交互模式：3 服务已后台运行，脚本退出（日志见 logs/*.log）"
fi
