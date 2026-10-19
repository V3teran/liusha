#!/usr/bin/env bash
# 实时监控脚本：监控数据库所有表、Redis、日志

set -euo pipefail

PG_CONTAINER="liusha-postgres"
REDIS_CONTAINER="liusha-redis"
LOG_DIR="logs"

echo "===== 开始监控 ====="
echo "时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

# 监控数据库所有表
echo "===== PostgreSQL 表监控 ====="
docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c "
SELECT
    schemaname,
    tablename,
    pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;
"

echo ""
echo "===== Task 表 ====="
docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c "
SELECT id, target_host, brief, status, created_at
FROM task
ORDER BY created_at DESC
LIMIT 5;
"

echo ""
echo "===== Agent Run 表 ====="
docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c "
SELECT id, role, status, created_at
FROM agent_run
ORDER BY created_at DESC
LIMIT 10;
"

echo ""
echo "===== Exploration Node 表 ====="
docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c "
SELECT id, kind, created_at
FROM exploration_node
ORDER BY created_at DESC
LIMIT 10;
"

echo ""
echo "===== Finding 表 ====="
docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c "
SELECT id, severity, cwe_id, summary, status, created_at
FROM finding
ORDER BY created_at DESC
LIMIT 10;
"

echo ""
echo "===== Insight 表 ====="
docker exec "$PG_CONTAINER" psql -U liusha -d liusha -c "
SELECT id, category, priority, confidence, severity, summary, created_at
FROM insight
ORDER BY created_at DESC
LIMIT 10;
"

echo ""
echo "===== Redis 监控 ====="
echo "Keys 总数:"
docker exec "$REDIS_CONTAINER" redis-cli DBSIZE

echo ""
echo "Asynq 队列:"
docker exec "$REDIS_CONTAINER" redis-cli --scan --pattern "asynq:*" | head -20

echo ""
echo "===== 日志监控 ====="
echo "Runner 日志最后 20 行:"
tail -20 "$LOG_DIR/runner.log" 2>/dev/null || echo "(runner.log 不存在)"

echo ""
echo "===== 监控完成 ====="
