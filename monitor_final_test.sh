#!/bin/bash
# 监控最终方案的 E2E 测试

TASK_ID="fa9bbb2f-8931-467b-a958-78b4ec7c714d"

echo "====== 最终方案 E2E 测试监控 ======"
echo "任务 ID: $TASK_ID"
echo "目标：验证 LLM 是否会主动判断停止"
echo ""

while true; do
    clear
    echo "====== $(date '+%H:%M:%S') ======"
    echo ""

    echo "【1. 节点统计】"
    docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT kind, COUNT(*)
        FROM wm_node
        WHERE task_id = '$TASK_ID'
        GROUP BY kind
        ORDER BY kind;
    " 2>/dev/null

    echo ""
    echo "【2. Objectives 数量】"
    OBJ_COUNT=$(docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT COUNT(*) FROM wm_node
        WHERE task_id = '$TASK_ID' AND kind = 'objective';
    " 2>/dev/null | xargs)
    echo "  总数: $OBJ_COUNT"

    echo ""
    echo "【3. 每个 Objective 的 Actions】"
    docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT
            n.id,
            COUNT(e.dst_id) as actions
        FROM wm_node n
        LEFT JOIN wm_edge e ON e.src_id = n.id AND e.rel = 'GENERATES'
        WHERE n.task_id = '$TASK_ID' AND n.kind = 'objective'
        GROUP BY n.id, n.created_at
        ORDER BY n.created_at;
    " 2>/dev/null

    echo ""
    echo "【4. TRIGGERS 边数量】"
    TRIGGERS=$(docker exec liusha-postgres psql -U liusha -d liusha -t -c "
        SELECT COUNT(*) FROM wm_edge
        WHERE task_id = '$TASK_ID' AND rel = 'TRIGGERS';
    " 2>/dev/null | xargs)
    echo "  $TRIGGERS 条"

    echo ""
    echo "【5. 关键观察】"
    if [ "$OBJ_COUNT" -gt "1" ]; then
        echo "  ✅ 已生成新 Objectives！"
    else
        echo "  ⏳ 仍在探索初始 Objective..."
    fi

    echo ""
    echo "按 Ctrl+C 停止监控"
    sleep 15
done
