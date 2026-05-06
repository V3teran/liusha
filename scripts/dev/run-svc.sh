#!/usr/bin/env bash
# scripts/dev/run-svc.sh — host 侧并行跑 vulnapp + proxy + api + scanner
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
export LIUSHA_API_ADDR="${LIUSHA_API_ADDR:-0.0.0.0:8090}"  # 8080 易被 Burp Suite Pro 占用，dev 默认 :8090
export LIUSHA_API_KEY="${LIUSHA_API_KEY:-changeme-dev-key}"
export LIUSHA_ENV="${LIUSHA_ENV:-development}"
export LIUSHA_LOG_LEVEL="${LIUSHA_LOG_LEVEL:-info}"
export LIUSHA_LOG_DIR="${LIUSHA_LOG_DIR:-logs}"
# 多进程并行跑：stdout 易刷屏，dev 默认只走 file，需要时 tail -F logs/*.log
export LIUSHA_LOG_TO_STDOUT="${LIUSHA_LOG_TO_STDOUT:-false}"
export LIUSHA_LOG_TO_FILE="${LIUSHA_LOG_TO_FILE:-true}"
export LIUSHA_CONFIG="${LIUSHA_CONFIG:-./config/config.yaml}"

# dev 覆盖：把 light/fallback/vision 全路由到 deepseek，绕开 ANTHROPIC/OPENAI key 校验
export LIUSHA_LLM_LIGHT_PROVIDER="${LIUSHA_LLM_LIGHT_PROVIDER:-deepseek}"
export LIUSHA_LLM_FALLBACK_PROVIDER="${LIUSHA_LLM_FALLBACK_PROVIDER:-deepseek}"
export LIUSHA_LLM_VISION_PROVIDER="${LIUSHA_LLM_VISION_PROVIDER:-deepseek}"

# proxy 进程参数
export LIUSHA_PROXY_LISTEN_ADDR="${LIUSHA_PROXY_LISTEN_ADDR:-0.0.0.0:8888}"
export LIUSHA_PROXY_HEALTHZ_ADDR="${LIUSHA_PROXY_HEALTHZ_ADDR:-:9091}"

# api key 校验（必填）
if [ -z "${DEEPSEEK_API_KEY:-}" ]; then
  echo "✗ DEEPSEEK_API_KEY 未设（请在 .env.local 中填入）"
  exit 1
fi

echo "===== 启动 4 个 host 服务 ====="
echo "  config:        $LIUSHA_CONFIG"
echo "  postgres:      $LIUSHA_POSTGRES_DSN"
echo "  redis:         $LIUSHA_REDIS_ADDR"
echo "  proxy:         $LIUSHA_PROXY_LISTEN_ADDR (mitm) / $LIUSHA_PROXY_HEALTHZ_ADDR (healthz)"
echo "  llm overrides: light=$LIUSHA_LLM_LIGHT_PROVIDER fallback=$LIUSHA_LLM_FALLBACK_PROVIDER vision=$LIUSHA_LLM_VISION_PROVIDER"
echo ""

# 预清旧 dev 进程：避免端口被旧 nohup/run-svc 进程占着导致新启动 fatal "address
# already in use"，进而出现"半新半旧"的混跑栈（曾踩坑：proxy/api 留旧版，
# scanner/vulnapp 用新版，调试极难）。先 SIGTERM 再 SIGKILL，全部静默 best-effort。
pkill -f 'cmd/(api|scanner|proxy|vulnapp)' 2>/dev/null || true
sleep 1
pkill -9 -f 'exe/(api|scanner|proxy|vulnapp)' 2>/dev/null || true
sleep 1

# 端口预检：5 个目标端口任一仍被占（不属于本脚本管理）→ 立即报错退出，
# 让用户先手动清。比"启动 4 个、其中 2 个 fatal 而 healthz 还过"友好得多。
PORT_BUSY=()
for p in 8001 8888 8090 9090 9091; do
  if lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
    PORT_BUSY+=("$p")
  fi
done
if [ "${#PORT_BUSY[@]}" -gt 0 ]; then
  echo "✗ 端口仍被占：${PORT_BUSY[*]}"
  echo "  → 先手动清：lsof -nP -iTCP:${PORT_BUSY[0]} -sTCP:LISTEN"
  exit 1
fi

# 启动顺序：vulnapp → proxy → api → scanner
# 应用层 logx 直接写 logs/{service}.log；shell 这里 stderr 兜底捕获 panic 前的早期输出
echo "[1/4] vulnapp on :8001"
go run ./cmd/vulnapp 2>logs/vulnapp.stderr &
VULNAPP_PID=$!

sleep 2
echo "[2/4] proxy on :8888 (mitm) + $LIUSHA_PROXY_HEALTHZ_ADDR (healthz)"
go run ./cmd/proxy 2>logs/proxy.stderr &
PROXY_PID=$!

sleep 2
echo "[3/4] api on $LIUSHA_API_ADDR"
go run ./cmd/api 2>logs/api.stderr &
API_PID=$!

sleep 2
echo "[4/4] scanner on :9090"
go run ./cmd/scanner 2>logs/scanner.stderr &
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
echo "✓ 四服务在跑（pids: vulnapp=${VULNAPP_PID} proxy=${PROXY_PID} api=${API_PID} scanner=${WORKER_PID}）"
echo "  日志合并 tail（Ctrl-C 关闭服务+退出 tail）："
echo ""

# logx 自管落盘：等待文件出现后再 tail（避免 tail 启动时 lumberjack 还没创建文件）
for f in logs/vulnapp.log logs/proxy.log logs/api.log logs/scanner.log; do
  for _ in $(seq 1 20); do
    [ -f "$f" ] && break
    sleep 0.5
  done
done

# tail -F 四个日志（go-stack panic 等会落到 *.stderr，需要时再单独看）
tail -F logs/vulnapp.log logs/proxy.log logs/api.log logs/scanner.log
