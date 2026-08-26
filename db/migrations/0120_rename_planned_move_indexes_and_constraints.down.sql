-- 0120 回滚：恢复索引和约束的旧名称

-- 恢复主键索引
ALTER INDEX planned_move_pkey RENAME TO execution_plan_pkey;

-- 恢复其他索引
ALTER INDEX planned_move_task_id_status_idx RENAME TO execution_plan_task_id_status_idx;
ALTER INDEX planned_move_task_id_priority_idx RENAME TO execution_plan_task_id_priority_idx;

-- 恢复约束
ALTER TABLE planned_move RENAME CONSTRAINT planned_move_domain_check TO execution_plan_domain_check;
ALTER TABLE planned_move RENAME CONSTRAINT planned_move_kind_check TO execution_plan_kind_check;
ALTER TABLE planned_move RENAME CONSTRAINT planned_move_status_check TO execution_plan_status_check;

-- 恢复注释
COMMENT ON TABLE planned_move IS 'Move 执行计划表：Planner 产出的待执行动作，独立于世界模型存储';
