-- 动态图性能监控视图
-- 用于监控 KnowledgeGraph 中 Action 的规模和状态分布

-- 创建监控视图：Action 规模统计
CREATE OR REPLACE VIEW action_scale_monitor AS
SELECT
    task_id,
    COUNT(*) as total_actions,
    COUNT(*) FILTER (WHERE state = 'open') as open_count,
    COUNT(*) FILTER (WHERE state = 'running') as running_count,
    COUNT(*) FILTER (WHERE state = 'done') as done_count,
    COUNT(*) FILTER (WHERE state = 'failed') as failed_count,
    COUNT(*) FILTER (WHERE state = 'blocked') as blocked_count,
    MAX(updated_at) as last_update,
    MIN(created_at) as first_action_created,
    -- 性能评估标记
    CASE
        WHEN COUNT(*) > 500 THEN '⚠️  超过阈值 - 建议评估 DAG 引擎'
        WHEN COUNT(*) > 100 THEN '⚡ 接近阈值 - 持续监控'
        ELSE '✅ 正常规模'
    END as scale_status
FROM kg_nodes
WHERE kind = 'action'
GROUP BY task_id;

-- 创建索引优化查询性能
CREATE INDEX IF NOT EXISTS idx_kg_nodes_kind_state_task
ON kg_nodes(kind, state, task_id)
WHERE kind = 'action';

-- 创建监控查询：高规模任务告警
CREATE OR REPLACE VIEW high_scale_tasks AS
SELECT
    task_id,
    total_actions,
    open_count,
    running_count,
    scale_status,
    last_update
FROM action_scale_monitor
WHERE total_actions > 100
ORDER BY total_actions DESC;

-- 使用示例
--
-- 查看所有任务的规模分布：
-- SELECT * FROM action_scale_monitor ORDER BY total_actions DESC;
--
-- 查看需要关注的高规模任务：
-- SELECT * FROM high_scale_tasks;
--
-- 实时监控特定任务：
-- SELECT * FROM action_scale_monitor WHERE task_id = 'xxx';
