#!/usr/bin/env bash
# scripts/dev/e2e.sh [profile…] — 跑 e2e 触发器（host 侧）；自动管理 dev 栈生命周期
# 流程：清空 db/redis → 关 service → 清 logs → 重启 service → 等 healthz → 跑 e2e
# 用法：
#   ./scripts/dev/e2e.sh             # 不带参 = 跑全部 profile（bac + sqli）
#   ./scripts/dev/e2e.sh bac         # 仅 bac
#   ./scripts/dev/e2e.sh sqli        # 仅 sqli
#   ./scripts/dev/e2e.sh bac sqli    # 多选
#
# 清空范围（每次执行都做一次）：
#   - postgres：9 张业务表 TRUNCATE（schema 保留）
#   - redis：FLUSHDB；并立即 XGROUP CREATE MKSTREAM 重建 ingestor consumer group
#   - logs：先关 service 再 rm —— 确保 lumberjack fd 释放，新 service 写干净 logs

set -euo pipefail
cd "$(dirname "$0")/../.."

PG_CONTAINER="${LIUSHA_PG_CONTAINER:-liusha-postgres}"
REDIS_CONTAINER="${LIUSHA_REDIS_CONTAINER:-liusha-redis}"
HEALTHZ_WAIT_SECONDS="${HEALTHZ_WAIT_SECONDS:-60}"

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

echo "===== 1/5 清空 db / redis ====="

# postgres：9 张业务表 TRUNCATE（schema 保留），任一表/容器不存在则失败但不阻断
if docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c \
    "TRUNCATE TABLE vuln_finding, host_lesson, llm_invocation, react_run, flow_decision, http_flow, graph_edge, graph_node, engagement CASCADE;" \
    >/dev/null 2>&1; then
  echo "  ✓ postgres 9 张业务表已 truncate"
else
  echo "  ⚠ postgres truncate 失败（容器 $PG_CONTAINER 不在？）"
fi

# redis FLUSHDB → 立即重建 ingestor consumer group
if docker exec "$REDIS_CONTAINER" redis-cli FLUSHDB >/dev/null 2>&1; then
  echo "  ✓ redis FLUSHDB"
  docker exec "$REDIS_CONTAINER" redis-cli XGROUP CREATE liusha:flow_events liusha-ingestor 0 MKSTREAM \
    >/dev/null 2>&1 || true
  echo "  ✓ ingestor consumer group 已重建（XGROUP CREATE MKSTREAM）"
else
  echo "  ⚠ redis FLUSHDB 失败（容器 $REDIS_CONTAINER 不在？）"
fi

echo ""
echo "===== 2/5 关旧 service（按端口找 PID） ====="
for port in 8001 8888 8090 9090 9091; do
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1) || true
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
  fi
done
sleep 1
for port in 8001 8888 8090 9090 9091; do
  pid=$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1) || true
  if [ -n "$pid" ]; then
    kill -9 "$pid" 2>/dev/null || true
  fi
done
sleep 1
echo "  ✓ 旧 service 已关停（端口 8001/8888/8090/9090/9091 释放）"

echo ""
echo "===== 3/5 清 logs（fd 已释放，rm 真正删除）====="
removed=0
shopt -s nullglob 2>/dev/null || true
for f in logs/*.log logs/*.stderr; do
  rm -f "$f" 2>/dev/null && removed=$((removed + 1))
done
echo "  ✓ logs 已清空（删 ${removed} 个文件）"

echo ""
echo "===== 4/5 重启 service ====="
nohup ./scripts/dev/run-svc.sh >/tmp/liusha-run-svc.out 2>&1 &
RUN_SVC_PID=$!
echo "  run-svc.sh background PID=${RUN_SVC_PID} (output: /tmp/liusha-run-svc.out)"

# 等 healthz 全部 200（最多 HEALTHZ_WAIT_SECONDS 秒）
echo "  等 healthz 全部上线（最多 ${HEALTHZ_WAIT_SECONDS}s）..."
deadline=$((SECONDS + HEALTHZ_WAIT_SECONDS))
while [ $SECONDS -lt $deadline ]; do
  api_ok=0; scanner_ok=0; proxy_ok=0; vulnapp_ok=0
  curl -sf -m 2 "${LIUSHA_API_BASE}/healthz" >/dev/null 2>&1 && api_ok=1
  curl -sf -m 2 http://localhost:9090/healthz >/dev/null 2>&1 && scanner_ok=1
  curl -sf -m 2 http://localhost:9091/healthz >/dev/null 2>&1 && proxy_ok=1
  nc -z localhost 8001 2>/dev/null && vulnapp_ok=1
  if [ "$((api_ok + scanner_ok + proxy_ok + vulnapp_ok))" -eq 4 ]; then
    echo "  ✓ 4 service 全部 healthy"
    break
  fi
  sleep 2
done
if [ $SECONDS -ge $deadline ]; then
  echo "  ✗ healthz 等待超时（${HEALTHZ_WAIT_SECONDS}s）— api=$api_ok scanner=$scanner_ok proxy=$proxy_ok vulnapp=$vulnapp_ok"
  echo "  → 看 /tmp/liusha-run-svc.out 与 logs/*.stderr"
  exit 1
fi

echo ""
if [ $# -eq 0 ]; then
  echo "===== 5/5 跑 e2e 触发器 profile=ALL（约 2-12 分钟，串行跑全部）====="
else
  echo "===== 5/5 跑 e2e 触发器 profile=[$*]（约 1-6 分钟/个）====="
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
