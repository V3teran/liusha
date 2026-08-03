#!/usr/bin/env bash
# scripts/dev/e2e.sh [profile…] — 跑 e2e 触发器（host 侧）；自动管理 dev 栈生命周期
# 流程：清空 db/redis → 关 service → 清 logs → 重启 service → 等 healthz → 跑 e2e
# 用法：
#   ./scripts/dev/e2e.sh                       # 不带参 = 跑全部 passive profile（按字典序：bac + brute + lfi + sqli + upload + xss + ...）
#   ./scripts/dev/e2e.sh bac                   # passive 仅 bac（业务向访问控制）
#   ./scripts/dev/e2e.sh sqli                  # passive 仅 sqli
#   ./scripts/dev/e2e.sh xss                   # passive 仅 xss
#   ./scripts/dev/e2e.sh brute                 # passive 仅 brute（暴力破解）
#   ./scripts/dev/e2e.sh lfi                   # passive 仅 lfi（文件包含/路径遍历/任意文件读取/CWE-22）
#   ./scripts/dev/e2e.sh upload                # passive 仅 upload（任意文件上传/CWE-434）
#   ./scripts/dev/e2e.sh bac sqli xss          # passive 多选（空格分隔）
#   ./scripts/dev/e2e.sh passive:upload,lfi    # passive 多选（passive: 前缀 + 逗号分隔，等价于 upload lfi）
#   ./scripts/dev/e2e.sh active:full           # active 模式：开放性 brief 压测 LLM 自主 recon + swarm 决策
#   ./scripts/dev/e2e.sh bac active:full       # passive + active 混合
#   LIUSHA_E2E_BRIEF="..." \
#     ./scripts/dev/e2e.sh active:adhoc        # active 一次性扫描——brief 从 env 注入（密码不入库）
#                                              # 可选 LIUSHA_E2E_MIN_FINDINGS=N 覆盖 PASS 门槛（默认 1）
#
# 清空范围（每次执行都做一次）：
#   - postgres：12 张业务表 TRUNCATE（schema 保留）
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
# TRUNCATE 是原子的会整体失败，旧数据残留 → session 复用、finding 累积、e2e 不可信。
# make migrate 幂等：已应用的 noop。
if make migrate 2>&1 | tail -5; then
  echo "  ✓ migrate 完成（含已应用的 noop）"
else
  echo "  ✗ migrate 失败 — 看上面输出"
  exit 1
fi

echo ""
echo "===== 2/6 清空 db / redis ====="

# postgres：业务表 TRUNCATE（schema 保留）。
# 错误**不再静默**——TRUNCATE 任一表失败会立即 exit，避免旧 task / finding 残留。
# 表名演化：0043 agent_run→agent_task，0054 agent_task→hunter；0046 加 tool_invocation，0047 加 audit_log；
# 0056 加 endpoint，0064 退役（攻击面改从流量派生 sitemap）；0059 删 finding_relation（→ finding.depends_on uuid[] 替代）；
# 0073-0077：active_scan+passive_session 合并为 task；http_flow 拆 proxy_traffic+agent_traffic；conversation.scan_id→task_id；
# 0078：assignment（下发容器）+ cron_schedule（定时模板）——assignment 是 task 的父表（task.assignment_id
# REFERENCES assignment.id），不显式 truncate 会在多次 e2e 运行间无限堆积孤儿行。
# 0079：lesson → corpus（跨目标知识库，hybrid RAG）——lesson 表已删，改 truncate corpus。
# task 放最后——CASCADE 会连带清 hunter/finding/... 的 task_id 引用行，但显式全列更清晰。
if ! docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c \
    "TRUNCATE TABLE finding, corpus, llm_invocation, tool_invocation, audit_log, hunter, proxy_traffic, agent_traffic, conversation, task, assignment, cron_schedule CASCADE;"; then
  echo "  ✗ postgres TRUNCATE 失败 — 看上面 psql 错误（常见原因：容器不在 / schema 不一致 / migrate 未跑）"
  exit 1
fi
echo "  ✓ postgres 业务表已 truncate"

# session-store/<owner_id>/ 是 ResultCompress middleware 的落盘目录；
# truncate 后 DB 中 session 已不存在，对应子目录变孤儿，清掉避免无限堆积。
rm -rf session-store/*/ 2>/dev/null || true

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
# pkill 兜底：runner 启动若 healthz :9090 端口冲突会保留 exe 进程但不绑端口
# → lsof 找不到 → 老 binary 留下与 asynq 抢任务（曾踩坑：上次 commit 67f1c24
# extractHostFromBrief 没生效就是因为老 runner 拿到了 task 用旧逻辑跑）。
# -9 强杀所有 runner exe + go run 父进程兜底。
pkill -9 -f 'exe/runner' 2>/dev/null || true
pkill -9 -f 'go run.*cmd/runner' 2>/dev/null || true
sleep 1
echo "  ✓ 旧 service 已关停（端口 8001/8888/8090/9090 释放 + runner 进程兜底 pkill）"

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
  api_ok=0; runner_ok=0; proxy_ok=0; vulnapp_ok=0
  curl -sf -m 2 "${LIUSHA_API_BASE}/healthz" >/dev/null 2>&1 && api_ok=1
  curl -sf -m 2 http://localhost:9090/healthz >/dev/null 2>&1 && runner_ok=1
  # proxy 无 healthz HTTP（纯 MITM）——探 TCP 8888 mitm 口是否在听。
  nc -z localhost 8888 2>/dev/null && proxy_ok=1
  nc -z localhost 8001 2>/dev/null && vulnapp_ok=1
  if [ "$((api_ok + runner_ok + proxy_ok + vulnapp_ok))" -eq 4 ]; then
    echo "  ✓ 4 service 全部 healthy"
    echo ""
    echo "  📊 前端：liusha-ui 独立仓 → pnpm dev（/api 代理到本 api，X-API-Key=${LIUSHA_API_KEY}）"
    echo "     task_id 跑完 e2e 后从 finding 表查："
    echo "       docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
    echo "         \"SELECT DISTINCT task_id FROM finding ORDER BY task_id;\""
    break
  fi
  sleep 2
done
if [ $SECONDS -ge $deadline ]; then
  echo "  ✗ healthz 等待超时（${HEALTHZ_WAIT_SECONDS}s）— api=$api_ok runner=$runner_ok proxy=$proxy_ok vulnapp=$vulnapp_ok"
  echo "  → 看 /tmp/liusha-run-svc.out 与 logs/*.stderr"
  exit 1
fi

echo ""
if [ $# -eq 0 ]; then
  echo "===== 6/6 跑 e2e 触发器 profile=ALL（约 2-12 分钟，串行跑全部）====="
else
  echo "===== 6/6 跑 e2e 触发器 profile=[$*]（约 1-6 分钟/个）====="
fi
# -mod=mod：步骤 1 的 `make migrate`（golang-migrate 经 go run）会把 migrate 提为 go.mod 显式
# require，但它是工具依赖未进 vendor/。若此处走默认 -mod=vendor 会因「required but not vendored」
# 失败。与 run-svc.sh 统一用 -mod=mod 跑 dev 命令，绕开 vendor 一致性校验。
go run -mod=mod ./cmd/e2e "$@"
RC=$?

echo ""
if [ $RC -eq 0 ]; then
  echo "🎉 e2e PASS"
  echo ""
  echo "📊 看图："
  echo "  前端：liusha-ui 独立仓 → pnpm dev（/api 代理到本 api）"
  echo "  task 列表："
  echo "    docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
  echo "      \"SELECT id, mode, target_host, brief, status FROM task ORDER BY created_at DESC;\""
  echo ""
  echo "  组合漏洞 chains（depends_on uuid[] 数组）："
  echo "    docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
  echo "      \"SELECT id, severity, substr(summary,1,40), depends_on FROM finding WHERE depends_on <> '{}' ORDER BY created_at DESC;\""
else
  echo "✗ e2e 失败（exit $RC = 失败的 profile 数）"
  echo "  排查："
  echo "    1. logs/runner.log 看 ReAct 循环是否跑"
  echo "    2. docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
  echo "       'SELECT kind,severity,confidence,title FROM finding ORDER BY created_at DESC;'"
  echo "    3. 前端看图：liusha-ui 独立仓 → pnpm dev（/api 代理到本 api）"
fi

exit $RC
