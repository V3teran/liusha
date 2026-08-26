#!/usr/bin/env bash
# scripts/dev/run-svc-binary.sh — 使用编译好的二进制文件启动服务（避免 go run 网络依赖）
# 应用层（logx）自管落盘到 logs/{service}.log（lumberjack 轮转），shell 不再重定向。
# Ctrl-C 全部关闭

set -euo pipefail
cd "$(dirname "$0")/../.."

mkdir -p logs

# load .env.local（DEEPSEEK_API_KEY 等）
if [ -f .env.local ]; then
  set -a
  # shellcheck disable=SC1091
  source .env.local
  set +a
fi

# host 侧地址（pg/redis 在 docker 暴露 5432/6379）
export LIUSHA_POSTGRES_DSN="${LIUSHA_POSTGRES_DSN:-postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable}"
export LIUSHA_REDIS_ADDR="${LIUSHA_REDIS_ADDR:-localhost:6379}"
export LIUSHA_API_ADDR="${LIUSHA_API_ADDR:-0.0.0.0:8090}"
export LIUSHA_API_KEY="${LIUSHA_API_KEY:-changeme-dev-key}"
export LIUSHA_STREAM_COOKIE_SECRET="${LIUSHA_STREAM_COOKIE_SECRET:-dev-stream-cookie-secret-not-for-prod}"
export LIUSHA_DEV_AUTOFILL="${LIUSHA_DEV_AUTOFILL:-1}"
export LIUSHA_ENV="${LIUSHA_ENV:-development}"
export LIUSHA_LOG_LEVEL="${LIUSHA_LOG_LEVEL:-info}"
export LIUSHA_LOG_DIR="${LIUSHA_LOG_DIR:-logs}"
export LIUSHA_LOG_TO_STDOUT="${LIUSHA_LOG_TO_STDOUT:-false}"
export LIUSHA_LOG_TO_FILE="${LIUSHA_LOG_TO_FILE:-true}"
export LIUSHA_CONFIG="${LIUSHA_CONFIG:-./config/config.yaml}"
export LIUSHA_PROXY_LISTEN_ADDR="${LIUSHA_PROXY_LISTEN_ADDR:-0.0.0.0:8888}"

# api key 校验（必填）
if [ -z "${XIAOMI_API_KEY:-}" ]; then
  echo "✗ XIAOMI_API_KEY 未设（请在 .env.local 中填入）"
  exit 1
fi

echo "===== 启动 4 个 host 服务（使用二进制文件）====="
echo "  config:        $LIUSHA_CONFIG"
echo "  postgres:      $LIUSHA_POSTGRES_DSN"
echo "  redis:         $LIUSHA_REDIS_ADDR"
echo "  proxy:         $LIUSHA_PROXY_LISTEN_ADDR (mitm, 无 healthz)"
echo ""

# 预清旧 dev 进程
for port in 8001 8888 8090 9090; do
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1) || true
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
  fi
done
sleep 1
for port in 8001 8888 8090 9090; do
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1) || true
  if [ -n "$pid" ]; then
    kill -9 "$pid" 2>/dev/null || true
  fi
done
sleep 1

# 端口预检
PORT_BUSY=()
for p in 8001 8888 8090 9090; do
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    PORT_BUSY+=("$p")
  fi
done
if [ "${#PORT_BUSY[@]}" -gt 0 ]; then
  echo "✗ 端口仍被占：${PORT_BUSY[*]}"
  echo "  → 先手动清：lsof -nP -iTCP:${PORT_BUSY[0]} -sTCP:LISTEN"
  exit 1
fi

# 检查二进制文件是否存在
if [ ! -x ./vulnapp ]; then
  echo "✗ ./vulnapp 不存在，请先编译：go build -o vulnapp ./cmd/vulnapp"
  exit 1
fi
if [ ! -x ./proxy ]; then
  echo "✗ ./proxy 不存在，请先编译：go build -o proxy ./cmd/proxy"
  exit 1
fi
if [ ! -x ./api ]; then
  echo "✗ ./api 不存在，请先编译：go build -o api ./cmd/api"
  exit 1
fi
if [ ! -x ./runner ]; then
  echo "✗ ./runner 不存在，请先编译：go build -o runner ./cmd/runner"
  exit 1
fi

# 启动顺序：vulnapp → proxy → api → runner
echo "[1/4] vulnapp on :8001"
./vulnapp 2>logs/vulnapp.stderr &
VULNAPP_PID=$!

sleep 2
echo "[2/4] proxy on :8888 (mitm)"
./proxy 2>logs/proxy.stderr &
PROXY_PID=$!

sleep 2
echo "[3/4] api on $LIUSHA_API_ADDR"
./api 2>logs/api.stderr &
API_PID=$!

sleep 2
echo "[4/4] runner on :9090"
./runner 2>logs/runner.stderr &
WORKER_PID=$!

# 等服务起来
sleep 3

# Ctrl-C 优雅关
cleanup() {
  echo ""
  echo "===== 关闭服务 ====="
  for pid in "$WORKER_PID" "$API_PID" "$PROXY_PID" "$VULNAPP_PID"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  wait 2>/dev/null || true
  echo "✓ 全部关闭"
}
trap cleanup INT TERM

echo ""
echo "✓ 四服务在跑（pids: vulnapp=${VULNAPP_PID} proxy=${PROXY_PID} api=${API_PID} runner=${WORKER_PID}）"
echo ""
echo "📊 前端：liusha-ui 独立仓 → cd 该仓 pnpm dev（/api 已代理到本 api）"
echo "  (X-API-Key=${LIUSHA_API_KEY}，task_id 跑完 e2e 后从 finding 表查)"
echo ""
echo "  日志合并 tail（Ctrl-C 关闭服务+退出 tail）："
echo ""

# 等待日志文件出现
for f in logs/vulnapp.log logs/proxy.log logs/api.log logs/runner.log; do
  for _ in $(seq 1 20); do
    [ -f "$f" ] && break
    sleep 0.5
  done
done

# tail -F 四个日志
tail -F logs/vulnapp.log logs/proxy.log logs/api.log logs/runner.log
