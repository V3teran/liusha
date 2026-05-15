#!/usr/bin/env bash
# scripts/dev/e2e.sh [profile…] — 跑 e2e 触发器（host 侧）；自动管理 dev 栈生命周期
# 流程：清空 db/redis → 关 service → 清 logs → 重启 service → 等 healthz → 跑 e2e
# 用法：
#   ./scripts/dev/e2e.sh                       # 不带参 = 跑全部 profile（按字典序：bac + brute + path-traversal + sqli + unrestricted-upload + xss）
#   ./scripts/dev/e2e.sh bac                   # 仅 bac（业务向访问控制）
#   ./scripts/dev/e2e.sh sqli                  # 仅 sqli
#   ./scripts/dev/e2e.sh xss                   # 仅 xss
#   ./scripts/dev/e2e.sh brute                 # 仅 brute（暴力破解）
#   ./scripts/dev/e2e.sh path-traversal        # 仅 path-traversal（任意文件读取/CWE-22）
#   ./scripts/dev/e2e.sh unrestricted-upload   # 仅 unrestricted-upload（任意文件上传/CWE-434）
#   ./scripts/dev/e2e.sh bac sqli xss          # 多选
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
export LIUSHA_VULNAPP_BASE="${LIUSHA_VULNAPP_BASE:-http://111.229.193.40:38001}"
export LIUSHA_POSTGRES_DSN="${LIUSHA_POSTGRES_DSN:-postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable}"

echo "===== 1/6 跑 migrate（确保 schema 跟得上代码改动）====="
# migrate 必须在 TRUNCATE 之前——否则代码里新增的表（如 finding_relation）尚未创建，
# TRUNCATE 是原子的会整体失败，旧数据残留 → engagement 复用、finding 累积、e2e 不可信。
# make migrate 幂等：已应用的 noop。
if make migrate 2>&1 | tail -5; then
  echo "  ✓ migrate 完成（含已应用的 noop）"
else
  echo "  ✗ migrate 失败 — 看上面输出"
  exit 1
fi

echo ""
echo "===== 2/6 清空 db / redis ====="

# postgres：8 张业务表 TRUNCATE（schema 保留）。
# 错误**不再静默**——TRUNCATE 任一表失败会立即 exit，避免旧 engagement / finding
# 残留导致 e2e 跑在污染数据上（曾踩坑：finding_relation 表未建时 TRUNCATE 整体回滚，
# 旧 engagement 被 LookupOrCreate 复用，agent_run_count 累积到 6）。
# finding_relation 排在 finding 之前防 FK 顺序问题（CASCADE 也兜底，显式列出更清晰）。
if ! docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c \
    "TRUNCATE TABLE finding_relation, finding, lesson, llm_invocation, agent_run, http_flow, engagement CASCADE;"; then
  echo "  ✗ postgres TRUNCATE 失败 — 看上面 psql 错误（常见原因：容器不在 / schema 不一致 / migrate 未跑）"
  exit 1
fi
echo "  ✓ postgres 7 张业务表已 truncate"

# engagement-store/<engagement_id>/ 是 ResultCompress middleware 的落盘目录；
# truncate 后 DB 中 engagement 已不存在，对应子目录变孤儿，清掉避免无限堆积。
rm -rf engagement-store/*/ 2>/dev/null || true

# redis FLUSHDB → 立即重建 ingestor consumer group
if docker exec "$REDIS_CONTAINER" redis-cli FLUSHDB >/dev/null 2>&1; then
  echo "  ✓ redis FLUSHDB"
  docker exec "$REDIS_CONTAINER" redis-cli XGROUP CREATE flow_events liusha-ingestor 0 MKSTREAM \
    >/dev/null 2>&1 || true
  echo "  ✓ ingestor consumer group 已重建（XGROUP CREATE MKSTREAM）"
else
  echo "  ⚠ redis FLUSHDB 失败（容器 $REDIS_CONTAINER 不在？）"
fi

echo ""
echo "===== 3/6 关旧 service（按端口找 PID） ====="
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
echo "===== 4/6 清 logs（fd 已释放，rm 真正删除）====="
removed=0
shopt -s nullglob 2>/dev/null || true
for f in logs/*.log logs/*.stderr; do
  rm -f "$f" 2>/dev/null && removed=$((removed + 1))
done
echo "  ✓ logs 已清空（删 ${removed} 个文件）"

echo ""
echo "===== 5/6 重启 service ====="
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
    echo ""
    echo "  📊 graph viewer：${LIUSHA_API_BASE}/viewer/index.html"
    echo "     在浏览器打开，填 engagement_id + X-API-Key (=${LIUSHA_API_KEY})，勾"每 5s 刷新"边扫边看。"
    echo "     engagement_id 跑完 e2e 后从 finding 表查："
    echo "       docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
    echo "         \"SELECT DISTINCT engagement_id FROM finding ORDER BY engagement_id;\""
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
  echo "===== 6/6 跑 e2e 触发器 profile=ALL（约 2-12 分钟，串行跑全部）====="
else
  echo "===== 6/6 跑 e2e 触发器 profile=[$*]（约 1-6 分钟/个）====="
fi
go run ./cmd/e2e "$@"
RC=$?

echo ""
if [ $RC -eq 0 ]; then
  echo "🎉 e2e PASS"
  echo ""
  echo "📊 看图："
  echo "  浏览器：${LIUSHA_API_BASE}/viewer/index.html"
  echo "  engagement_id 列表："
  echo "    docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
  echo "      \"SELECT id, mode, scope, status, expires_at FROM engagement ORDER BY created_at DESC;\""
  echo ""
  echo "  组合漏洞 enables 边："
  echo "    docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
  echo "      \"SELECT from_finding_id, to_finding_id, payload->>'reason' FROM finding_relation;\""
else
  echo "✗ e2e 失败（exit $RC = 失败的 profile 数）"
  echo "  排查："
  echo "    1. logs/scanner.log 看 ReAct 循环是否跑"
  echo "    2. docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
  echo "       'SELECT kind,severity,confidence,title FROM finding ORDER BY created_at DESC;'"
  echo "    3. graph viewer：${LIUSHA_API_BASE}/viewer/index.html （即使失败也能看到部分图）"
fi

exit $RC
