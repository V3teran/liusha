#!/usr/bin/env bash
# 持续监控脚本：每 30 秒检查一次数据库和日志变化

set -euo pipefail

PG_CONTAINER="liusha-postgres"
REDIS_CONTAINER="liusha-redis"
LOG_DIR="logs"
INTERVAL=30

echo "===== 启动持续监控（每 ${INTERVAL}s 检查）====="
echo "按 Ctrl+C 停止"
echo ""

while true; do
    clear
    echo "===== 监控时间: $(date '+%Y-%m-%d %H:%M:%S') ====="
    echo ""

    # Task 状态
    echo "【Task 状态】"
    docker exec "$PG_CONTAINER" psql -U liusha -d liusha -t -c "
        SELECT
            CONCAT(
                'ID: ', LEFT(id::text, 8),
                ' | Host: ', COALESCE(target_host, 'N/A'),
                ' | Status: ', status,
                ' | Created: ', created_at::timestamp(0)
            )
        FROM task
        ORDER BY created_at DESC
        LIMIT 3;
    " 2>/dev/null || echo "  (查询失败)"

    echo ""
    echo "【Agent Run 状态】"
    docker exec "$PG_CONTAINER" psql -U liusha -d liusha -t -c "
        SELECT
            CONCAT(
                LEFT(id::text, 8),
                ' | ', role,
                ' | ', status,
                ' | ', created_at::timestamp(0)
            )
        FROM agent_run
        ORDER BY created_at DESC
        LIMIT 5;
    " 2>/dev/null || echo "  (查询失败)"

    echo ""
    echo "【Agent Run 统计】"
    docker exec "$PG_CONTAINER" psql -U liusha -d liusha -t -c "
        SELECT
            role,
            status,
            COUNT(*) as count
        FROM agent_run
        GROUP BY role, status
        ORDER BY role, status;
    " 2>/dev/null || echo "  (查询失败)"

    echo ""
    echo "【Finding 统计】"
    docker exec "$PG_CONTAINER" psql -U liusha -d liusha -t -c "
        SELECT
            severity,
            status,
            COUNT(*) as count
        FROM finding
        GROUP BY severity, status
        ORDER BY
            CASE severity
                WHEN 'critical' THEN 1
                WHEN 'high' THEN 2
                WHEN 'medium' THEN 3
                WHEN 'low' THEN 4
            END;
    " 2>/dev/null || echo "  (暂无 Finding)"

    echo ""
    echo "【Exploration Node 统计】"
    docker exec "$PG_CONTAINER" psql -U liusha -d liusha -t -c "
        SELECT kind, COUNT(*)
        FROM exploration_node
        GROUP BY kind
        ORDER BY COUNT(*) DESC;
    " 2>/dev/null || echo "  (暂无节点)"

    echo ""
    echo "【Insight 统计】"
    docker exec "$PG_CONTAINER" psql -U liusha -d liusha -t -c "
        SELECT category, priority, COUNT(*)
        FROM insight
        GROUP BY category, priority
        ORDER BY category, priority;
    " 2>/dev/null || echo "  (暂无 Insight)"

    echo ""
    echo "【Redis 队列】"
    echo -n "  Asynq pending: "
    docker exec "$REDIS_CONTAINER" redis-cli LLEN "asynq:{default}:pending" 2>/dev/null || echo "N/A"
    echo -n "  Asynq active: "
    docker exec "$REDIS_CONTAINER" redis-cli ZCARD "asynq:{default}:active" 2>/dev/null || echo "N/A"

    echo ""
    echo "【最新 Runner 日志】"
    tail -5 "$LOG_DIR/runner.log" 2>/dev/null | grep -E "level|message" | tail -3 || echo "  (无日志)"

    echo ""
    echo "===== 下次更新: $(date -v+${INTERVAL}S '+%H:%M:%S') ====="

    sleep "$INTERVAL"
done
