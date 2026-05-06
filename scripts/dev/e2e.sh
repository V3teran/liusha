#!/usr/bin/env bash
# scripts/dev/e2e.sh [profile] — 跑 e2e 触发器（host 侧），需要 run-svc.sh 在另一个终端跑着
# 流程：建 engagement → 录凭证 → 按 profile 发样本流量 → 轮询 finding 直到达标
# profile：bac（默认）/ sqli；新漏洞类型在 cmd/e2e 的 profiles map 加一行即可

set -euo pipefail
cd "$(dirname "$0")/../.."

PROFILE="${1:-bac}"

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
echo "===== 跑 e2e 触发器 profile=${PROFILE}（约 1-6 分钟）====="
LIUSHA_E2E_PROFILE="$PROFILE" go run ./cmd/e2e
RC=$?

echo ""
if [ $RC -eq 0 ]; then
  echo "🎉 e2e PASS（profile=${PROFILE}）"
else
  echo "✗ e2e 失败（exit $RC, profile=${PROFILE}）"
  echo "  排查："
  echo "    1. logs/scanner.log 看 ReAct 循环是否跑"
  echo "    2. docker exec liusha-postgres psql -U liusha -d liusha -c 'SELECT kind,severity,dedup_key FROM vuln_finding;'"
fi

exit $RC
