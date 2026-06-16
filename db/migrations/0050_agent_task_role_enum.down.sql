-- 0050 down: 回滚 agent_task.role 三角色 enum 到 v0049 的 "hunter" 单值。

-- 1. 删 CHECK 约束
ALTER TABLE agent_task DROP CONSTRAINT IF EXISTS agent_task_role_check;

-- 2. 回填三角色为 hunter（信息丢失：orchestrator/exploitation/traffic-analysis 区分回不来，
--    重新跑 0050 up 会按 owner_type + parent_id 推回）
UPDATE agent_task SET role = 'hunter'
    WHERE role IN ('traffic-analysis', 'orchestrator', 'exploitation');

COMMENT ON COLUMN agent_task.role IS NULL;
