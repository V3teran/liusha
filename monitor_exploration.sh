#!/bin/bash
# 监控 E2E 测试中的 Objective 和 Action 分布

TASK_ID="9757128f-17c8-49f6-b31a-94fdc1e7fe18"

echo "====== 持续监控探索进度 ======"
echo "任务 ID: $TASK_ID"
echo ""

while true; do
    clear
    echo "====== $(date '+%H:%M:%S') ======"
    echo ""

    echo "【节点统计】"
    docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT kind, COUNT(*)
        FROM wm_node
        WHERE task_id = '$TASK_ID'
        GROUP BY kind
        ORDER BY kind;
    " 2>/dev/null

    echo ""
    echo "【Objectives 列表】"
    docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT
            id,
            substr(content::jsonb->>'description', 1, 50) as description,
            created_at
        FROM wm_node
        WHERE task_id = '$TASK_ID' AND kind = 'objective'
        ORDER BY created_at;
    " 2>/dev/null

    echo ""
    echo "【每个 Objective 的 Actions 数量】"
    docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT
            e.src_id as objective_id,
            COUNT(e.dst_id) as action_count
        FROM wm_edge e
        WHERE e.task_id = '$TASK_ID'
          AND e.rel = 'GENERATES'
          AND e.src_id IN (
              SELECT id FROM wm_node
              WHERE task_id = '$TASK_ID' AND kind = 'objective'
          )
        GROUP BY e.src_id
        ORDER BY MIN(e.created_at);
    " 2>/dev/null

    echo ""
    echo "【TRIGGERS 边】"
    docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT src_id, dst_id, created_at
        FROM wm_edge
        WHERE task_id = '$TASK_ID' AND rel = 'TRIGGERS'
        ORDER BY created_at;
    " 2>/dev/null || echo "  (尚无 TRIGGERS 边)"

    echo ""
    echo "按 Ctrl+C 停止监控"
    sleep 10
done
