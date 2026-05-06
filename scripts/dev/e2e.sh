#!/usr/bin/env bash
# scripts/dev/e2e.sh [profile…] — 跑 e2e 触发器（host 侧），需要 run-svc.sh 在另一个终端跑着
# 流程：清空 db/redis/logs → 健康检查 → 启动期一次预录全部 host 凭证 → 串行跑选中的 profile
# 用法：
#   ./scripts/dev/e2e.sh             # 不带参 = 跑全部 profile（bac + sqli）
#   ./scripts/dev/e2e.sh bac         # 仅 bac
#   ./scripts/dev/e2e.sh sqli        # 仅 sqli
#   ./scripts/dev/e2e.sh bac sqli    # 多选
#
# 清空范围：
#   - postgres：9 张业务表 TRUNCATE（schema 保留）
#   - redis：FLUSHDB；并立即 XGROUP CREATE MKSTREAM 重建 ingestor consumer group
#     （否则 scanner Run loop 会一直报 NOGROUP，因为它没自动重建逻辑）
#   - logs：清 logs/*.log + logs/*.stderr（lumberjack 重新落盘）

set -euo pipefail
cd "$(dirname "$0")/../.."

PG_CONTAINER="${LIUSHA_PG_CONTAINER:-liusha-postgres}"
REDIS_CONTAINER="${LIUSHA_REDIS_CONTAINER:-liusha-redis}"

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
echo "===== 清空 db / redis / logs ====="

# postgres：9 张业务表 TRUNCATE（schema 保留），任一表/容器不存在则失败但不阻断
if docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c \
    "TRUNCATE TABLE vuln_finding, host_lesson, llm_invocation, react_run, flow_decision, http_flow, graph_edge, graph_node, engagement CASCADE;" \
    >/dev/null 2>&1; then
  echo "  ✓ postgres 9 张业务表已 truncate"
else
  echo "  ⚠ postgres truncate 失败（容器 $PG_CONTAINER 不在？）"
fi

# redis FLUSHDB → 立即重建 ingestor consumer group（不重建 scanner 会一直报 NOGROUP）
if docker exec "$REDIS_CONTAINER" redis-cli FLUSHDB >/dev/null 2>&1; then
  echo "  ✓ redis FLUSHDB"
  docker exec "$REDIS_CONTAINER" redis-cli XGROUP CREATE liusha:flow_events liusha-ingestor 0 MKSTREAM \
    >/dev/null 2>&1 || true
  echo "  ✓ ingestor consumer group 已重建（XGROUP CREATE MKSTREAM）"
else
  echo "  ⚠ redis FLUSHDB 失败（容器 $REDIS_CONTAINER 不在？）"
fi

# logs 清空（lumberjack 会自动重新落盘）
rm -f logs/*.log logs/*.stderr 2>/dev/null && echo "  ✓ logs 已清空"

echo ""
if [ $# -eq 0 ]; then
  echo "===== 跑 e2e 触发器 profile=ALL（约 2-12 分钟，串行跑全部）====="
else
  echo "===== 跑 e2e 触发器 profile=[$*]（约 1-6 分钟/个）====="
fi
go run ./cmd/e2e "$@"
RC=$?

echo ""
if [ $RC -eq 0 ]; then
  echo "🎉 e2e PASS"
else
  echo "✗ e2e 失败（exit $RC = 失败的 profile 数）"
  echo "  排查："
  echo "    1. logs/scanner.log 看 ReAct 循环是否跑"
  echo "    2. docker exec liusha-postgres psql -U liusha -d liusha -c 'SELECT kind,severity,confidence,title FROM vuln_finding ORDER BY created_at DESC;'"
fi

exit $RC
