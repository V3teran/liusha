#!/usr/bin/env bash
# scripts/dev/e2e.sh [profile…] — 跑 e2e 触发器（host 侧）；自动管理 dev 栈生命周期
# 流程：清空 db/redis → 关 service → 清 logs → 重启 service → 等 healthz → 跑 e2e
# 用法（剧本定义见 cmd/e2e/profiles.go）：
#   ./scripts/dev/e2e.sh             # 不带参 = 按字典序跑全部剧本
#   ./scripts/dev/e2e.sh xss         # XSS 专项扫描
#   ./scripts/dev/e2e.sh full        # 综合开放性扫描：开放性 brief 压测 LLM 自主 recon
#   ./scripts/dev/e2e.sh xss full    # 多选（空格分隔，按参数顺序串行）
#
# 清空范围（每次执行都做一次）：
#   - postgres：运行时表动态 TRUNCATE（豁免用户配置/登录资产/迁移台账）
#   - redis：FLUSHDB；并立即 XGROUP CREATE MKSTREAM 重建 ingestor consumer group
#   - logs：先关 service 再 rm —— 确保 lumberjack fd 释放，新 service 写干净 logs

set -euo pipefail
cd "$(dirname "$0")/../.."

PG_CONTAINER="${LIUSHA_PG_CONTAINER:-liusha-postgres}"
REDIS_CONTAINER="${LIUSHA_REDIS_CONTAINER:-liusha-redis}"
HEALTHZ_WAIT_SECONDS="${HEALTHZ_WAIT_SECONDS:-120}"  # 含编译 liusha 的时间

# load .env
if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  source .env
  set +a
fi

# host 侧地址
export LIUSHA_API_BASE="${LIUSHA_API_BASE:-http://localhost:8580}"  # 与 run-svc.sh 默认端口一致
export LIUSHA_POSTGRES_DSN="${LIUSHA_POSTGRES_DSN:-postgres://liusha:${POSTGRES_PASSWORD}@localhost:${POSTGRES_HOST_PORT:-55432}/liusha?sslmode=disable}"

# LLM Provider API Keys（从 .env 继承，显式 export 给子进程）
# LLM key（多把）随 .env 透传；redis 密码必填
export LIUSHA_REDIS_PASSWORD="${REDIS_PASSWORD}"
export LIUSHA_LLM_KEY_SECRET="${LIUSHA_LLM_KEY_SECRET}"

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

# postgres：运行时表 TRUNCATE（schema 保留）。
# 表清单从 information_schema 动态枚举——手写清单会随 schema 演化腐烂。
# 豁免三类（schema 变更时人工复核这份清单）：
#   - schema_migrations：golang-migrate 台账
#   - 配置：用户对 agent/技能/系统设置的自定义（agent_override、agent_prompt_archive、
#     skill_override、custom_skill、setting_override）、LLM 接入（llm_provider、
#     llm_role_route）与工具目录（tool）——清掉等于让操作者重新配置一遍
#   - 登录资产（account/account_session）：清了把操作者锁在门外
# 其余（task/finding/conversation/llm_invocation/message/...）全部清空，
# CASCADE 抹平 FK 顺序，RESTART IDENTITY 让 seq 从 1 重新计数。
EXCLUDED="'schema_migrations','agent_override','agent_prompt_archive','skill_override','custom_skill','setting_override','tool','llm_provider','llm_role_route','account','account_session'"
TABLES=$(docker exec "$PG_CONTAINER" psql -U liusha -d liusha -tAc \
  "SELECT string_agg(table_name, ', ' ORDER BY table_name) FROM information_schema.tables \
   WHERE table_schema = 'public' AND table_name NOT IN (${EXCLUDED})")
if [ -z "$TABLES" ]; then
  echo "  ✗ 动态枚举业务表为空——schema 异常，中止"
  exit 1
fi
if ! docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c \
    "TRUNCATE TABLE ${TABLES} RESTART IDENTITY CASCADE;"; then
  echo "  ✗ postgres TRUNCATE 失败 — 看上面 psql 错误（常见原因：容器不在 / schema 不一致 / migrate 未跑）"
  exit 1
fi
echo "  ✓ postgres 业务表已 truncate（$(echo "$TABLES" | awk -F', ' '{print NF}') 张）"

# redis FLUSHDB → 立即重建 ingestor consumer group
if docker exec "$REDIS_CONTAINER" redis-cli -a "$REDIS_PASSWORD" FLUSHDB >/dev/null 2>&1; then
  echo "  ✓ redis FLUSHDB"
  docker exec "$REDIS_CONTAINER" redis-cli -a "$REDIS_PASSWORD" --no-auth-warning XGROUP CREATE liusha:traffic_snapshots liusha-ingestor 0 MKSTREAM \
    >/dev/null 2>&1 || true
  echo "  ✓ ingestor consumer group 已重建（XGROUP CREATE MKSTREAM）"
else
  echo "  ⚠ redis FLUSHDB 失败（容器 $REDIS_CONTAINER 不在？）"
fi

echo ""
echo "===== 3/6 关旧 service（按端口找 PID） ====="
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
# pkill 兜底：runner 启动若 healthz :8590 端口冲突会保留进程但不绑端口
# → lsof 找不到 → 老进程留下与 asynq 抢任务。-9 强杀全部 runner 进程。
pkill -9 -f 'liusha runner' 2>/dev/null || true
# run-svc.sh 包装进程残留回收：其结尾 tail -F 会让脚本在端口清理后仍存活，
# 跨轮累积（其 cleanup trap 会按 PID 清理各自子进程，先 SIGTERM 再 -9）
pkill -f 'dev/run-svc.sh' 2>/dev/null || true
sleep 1
pkill -9 -f 'dev/run-svc.sh' 2>/dev/null || true
sleep 1
echo "  ✓ 旧 service 已关停（端口 8588/8580/8590 释放 + runner 进程兜底 pkill）"

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
  api_ok=0; runner_ok=0; proxy_ok=0
  curl -sf -m 2 "${LIUSHA_API_BASE}/healthz" >/dev/null 2>&1 && api_ok=1
  curl -sf -m 2 http://localhost:8590/healthz >/dev/null 2>&1 && runner_ok=1
  # proxy 无 healthz HTTP（纯 MITM）——探 TCP 8588 mitm 口是否在听。
  nc -z localhost 8588 2>/dev/null && proxy_ok=1
  if [ "$((api_ok + runner_ok + proxy_ok))" -eq 3 ]; then
    echo "  ✓ 3 service 全部 healthy"
    echo ""
    echo "  📊 前端：web/（本仓）→ make run-web（/api 代理到本 api；登录 admin / $LIUSHA_ADMIN_INITIAL_PASSWORD，dev 默认 changeme-admin-pass）"
    echo "     task_id 跑完 e2e 后从 finding 表查："
    echo "       docker exec ${PG_CONTAINER} psql -U liusha -d liusha -c \\"
    echo "         \"SELECT DISTINCT task_id FROM finding ORDER BY task_id;\""
    break
  fi
  sleep 2
done
if [ $SECONDS -ge $deadline ]; then
  echo "  ✗ healthz 等待超时（${HEALTHZ_WAIT_SECONDS}s）— api=$api_ok runner=$runner_ok proxy=$proxy_ok"
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
  echo "  前端：web/（本仓）→ make run-web（/api 代理到本 api）"
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
  echo "    3. 前端看图：web/（本仓）→ make run-web（/api 代理到本 api）"
fi

exit $RC
