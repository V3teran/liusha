#!/usr/bin/env bash
# scripts/dev/run-svc.sh — host 侧并行跑 proxy + api + runner
# 日志由 liusha 写到数据目录的 logs/{组件}.log（与沙盒容器同一目录）；shell 只把 stderr 兜到同目录
# 的 {组件}.stderr（捕获配置加载失败、panic 等早期输出）。Ctrl-C 全部关闭

set -euo pipefail
cd "$(dirname "$0")/../.."

# .env 与宿主侧连接、数据目录、配置文件（与沙盒容器同一份）。
# shellcheck source=scripts/dev/env.sh
. scripts/dev/env.sh
LOG_DIR="$LIUSHA_DATA_DIR/logs"
mkdir -p "$LOG_DIR"

# 多进程并行跑：stdout 易刷屏，dev 只写文件，需要时 tail -F "$LOG_DIR"/*.log
export LIUSHA_LOG_TO_STDOUT=false
export LIUSHA_LOG_TO_FILE=true

# 其余参数取配置文件（api :8580、proxy :8588、runner :8590）；临时改一项可用 LIUSHA_<键> 环境变量。
# LLM 接入在前端「LLM 配置」页维护：未接入时服务照常启动，任务下发会被拒绝并提示到前端配置。

echo "===== 编译 liusha ====="
go build -o bin/liusha ./cmd/liusha

echo "===== 启动 3 个 host 服务 ====="
echo "  postgres:      localhost:${LIUSHA_POSTGRES_HOST_PORT:-55432}"
echo "  redis:         localhost:${LIUSHA_REDIS_HOST_PORT:-56379}"
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
# liusha 写 $LOG_DIR/{组件}.log；shell 这里 stderr 兜底捕获配置加载失败、panic 前的早期输出
echo "[1/3] proxy on :8588 (mitm)"
bin/liusha proxy 2>"$LOG_DIR/proxy.stderr" &
PROXY_PID=$!

echo "[2/3] api on :8580"
bin/liusha api 2>"$LOG_DIR/api.stderr" &
API_PID=$!

echo "[3/3] runner on :8590"
bin/liusha runner 2>"$LOG_DIR/runner.stderr" &
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
echo "  日志合并 tail（$LOG_DIR，Ctrl-C 关闭服务+退出 tail）："
echo ""

# liusha 自管落盘：等待文件出现后再 tail（避免 tail 启动时日志文件还没创建）
for f in "$LOG_DIR/proxy.log" "$LOG_DIR/api.log" "$LOG_DIR/runner.log"; do
  for _ in $(seq 1 20); do
    [ -f "$f" ] && break
    sleep 0.5
  done
done

# 交互 TTY：tail -F 三个日志（Ctrl-C 触发上方 cleanup 优雅关停）。
# 非交互（nohup/CI，如 e2e.sh 拉起）：tail 会把脚本钉死成僵尸包装进程，
# 且多轮累积的 tail 向同一 stdout 文件灌重复流——直接退出，服务已后台运行。
if [ -t 1 ]; then
  tail -F "$LOG_DIR/proxy.log" "$LOG_DIR/api.log" "$LOG_DIR/runner.log"
else
  echo "✓ 非交互模式：3 服务已后台运行，脚本退出（日志见 $LOG_DIR/*.log）"
fi
