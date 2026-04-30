#!/usr/bin/env bash
# scripts/dev/e2e.sh — 跑 e2e-bac 触发器（host 侧），需要 run-svc.sh 在另一个终端跑着
# 流程：建 engagement → 录凭证 → 18 代理请求 → 轮询 finding + 4 项黑客松断言

set -euo pipefail
cd "$(dirname "$0")/../.."

# load .env.local
if [ -f .env.local ]; then
  set -a
  # shellcheck disable=SC1091
  source .env.local
  set +a
fi

# host 侧地址
export LIUSHA_API_BASE="${LIUSHA_API_BASE:-http://localhost:8090}"  # 与 run-svc.sh 默认端口一致
export LIUSHA_API_KEY="${LIUSHA_API_KEY:-changeme-dev-key}"
export LIUSHA_PROXY_ADDR="${LIUSHA_PROXY_ADDR:-http://localhost:8888}"
export LIUSHA_VULNAPP_BASE="${LIUSHA_VULNAPP_BASE:-http://localhost:8001}"
export LIUSHA_POSTGRES_DSN="${LIUSHA_POSTGRES_DSN:-postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable}"

echo "===== 前置检查 ====="

# 1. api 是否健康
if ! curl -sf -H "X-API-Key: $LIUSHA_API_KEY" "${LIUSHA_API_BASE}/healthz" >/dev/null; then
  echo "✗ api 不可达：${LIUSHA_API_BASE}/healthz"
  echo "  → 先在另一个终端跑 ./scripts/dev/run-svc.sh"
  exit 1
fi
echo "  ✓ api healthy"

# 2. vulnapp 是否可达（vulnapp 没 healthz，用 / 探测）
if ! curl -sf "${LIUSHA_VULNAPP_BASE}/" -o /dev/null -m 2 2>/dev/null && \
   ! curl -s "${LIUSHA_VULNAPP_BASE}/" -o /dev/null -m 2 2>/dev/null; then
  echo "✗ vulnapp 不可达：${LIUSHA_VULNAPP_BASE}"
  exit 1
fi
echo "  ✓ vulnapp 可达"

# 3. scanner healthz
if ! curl -sf http://localhost:9090/healthz >/dev/null; then
  echo "✗ scanner 不可达 :9090"
  exit 1
fi
echo "  ✓ scanner healthy"

# 4. proxify 8888 端口（docker）
if ! nc -z localhost 8888 2>/dev/null; then
  echo "⚠ proxify 8888 端口不通（继续，但 proxify 链路不会跑通）"
else
  echo "  ✓ proxify 端口可达"
fi

echo ""
echo "===== 跑 e2e-bac 触发器（约 6 分钟）====="
go run ./cmd/e2e-bac
RC=$?

echo ""
if [ $RC -eq 0 ]; then
  echo "🎉 e2e PASS（≥5 finding + 黑客松 4 项断言全过）"
else
  echo "✗ e2e 失败（exit $RC）"
  echo "  排查："
  echo "    1. logs/scanner.log 看 ReAct 循环是否跑"
  echo "    2. logs/api.log 看 sniffer enqueue 是否成功"
  echo "    3. docker exec liusha-postgres psql -U liusha -d liusha -c 'SELECT kind,severity,dedup_key FROM finding;'"
fi

exit $RC
