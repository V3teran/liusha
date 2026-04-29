#!/usr/bin/env bash
# scripts/dev/run-svc.sh — host 侧并行跑 vulnapp + api + agent-worker
# 三个进程合并到 logs/，Ctrl-C 全部关闭

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
export LIUSHA_API_ADDR="${LIUSHA_API_ADDR:-0.0.0.0:8080}"
export LIUSHA_API_KEY="${LIUSHA_API_KEY:-changeme-dev-key}"
export LIUSHA_ENV="${LIUSHA_ENV:-development}"
export LIUSHA_LOG_LEVEL="${LIUSHA_LOG_LEVEL:-info}"
export LIUSHA_CONFIG="${LIUSHA_CONFIG:-./config/config.yaml}"

# dev 覆盖：把 light/fallback/vision 全路由到 deepseek，绕开 ANTHROPIC/OPENAI key 校验
export LIUSHA_LLM_LIGHT_PROVIDER="${LIUSHA_LLM_LIGHT_PROVIDER:-deepseek}"
export LIUSHA_LLM_FALLBACK_PROVIDER="${LIUSHA_LLM_FALLBACK_PROVIDER:-deepseek}"
export LIUSHA_LLM_VISION_PROVIDER="${LIUSHA_LLM_VISION_PROVIDER:-deepseek}"

# proxify_consumer：默认不启动（host 侧暂不读 docker volume 中的 jsonl）
# e2e 测试用 LIUSHA_PROXIFY_JSONL=./logs/flows.jsonl 后续再支持
export LIUSHA_PROXIFY_JSONL="${LIUSHA_PROXIFY_JSONL:-}"

# api key 校验（必填）
if [ -z "${DEEPSEEK_API_KEY:-}" ]; then
  echo "✗ DEEPSEEK_API_KEY 未设（请在 .env.local 中填入）"
  exit 1
fi

echo "===== 启动 3 个 host 服务 ====="
echo "  config:        $LIUSHA_CONFIG"
echo "  postgres:      $LIUSHA_POSTGRES_DSN"
echo "  redis:         $LIUSHA_REDIS_ADDR"
echo "  llm overrides: light=$LIUSHA_LLM_LIGHT_PROVIDER fallback=$LIUSHA_LLM_FALLBACK_PROVIDER vision=$LIUSHA_LLM_VISION_PROVIDER"
echo ""

# 启动顺序：vulnapp → api → agent-worker（让 agent-worker 启动时其他依赖已健康）
echo "[1/3] vulnapp on :8001"
go run ./cmd/vulnapp >logs/vulnapp.log 2>&1 &
VULNAPP_PID=$!

sleep 2
echo "[2/3] api on :8080"
go run ./cmd/api >logs/api.log 2>&1 &
API_PID=$!

sleep 2
echo "[3/3] agent-worker on :9090"
go run ./cmd/agent-worker >logs/agent-worker.log 2>&1 &
WORKER_PID=$!

# 等服务起来
sleep 3

# Ctrl-C 优雅关
cleanup() {
  echo ""
  echo "===== 关闭服务 ====="
  for pid in "$WORKER_PID" "$API_PID" "$VULNAPP_PID"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  wait 2>/dev/null || true
  echo "✓ 全部关闭"
}
trap cleanup INT TERM

echo ""
echo "✓ 三服务在跑（pids: vulnapp=$VULNAPP_PID api=$API_PID agent-worker=$WORKER_PID）"
echo "  日志合并 tail（Ctrl-C 关闭服务+退出 tail）："
echo ""

# tail -F 三个日志
tail -F logs/vulnapp.log logs/api.log logs/agent-worker.log
