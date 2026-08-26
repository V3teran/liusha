-- 0120: 重命名 planned_move 表的索引和约束
-- 表名已经是 planned_move（手动修改），现在统一索引和约束命名

-- 重命名主键索引
ALTER INDEX execution_plan_pkey RENAME TO planned_move_pkey;

-- 重命名其他索引
ALTER INDEX execution_plan_task_id_status_idx RENAME TO planned_move_task_id_status_idx;
ALTER INDEX execution_plan_task_id_priority_idx RENAME TO planned_move_task_id_priority_idx;

-- 重命名约束
ALTER TABLE planned_move RENAME CONSTRAINT execution_plan_domain_check TO planned_move_domain_check;
ALTER TABLE planned_move RENAME CONSTRAINT execution_plan_kind_check TO planned_move_kind_check;
ALTER TABLE planned_move RENAME CONSTRAINT execution_plan_status_check TO planned_move_status_check;

-- 更新注释
COMMENT ON TABLE planned_move IS 'Planner 产出的待执行 Move，支持异步 Plan-Execute 循环';
