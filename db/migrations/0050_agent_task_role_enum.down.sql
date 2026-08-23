-- 0050 down: 回滚 agent_task.role 三角色 enum 到 v0049 的 "agent" 单值。

-- 1. 删 CHECK 约束
ALTER TABLE agent_task DROP CONSTRAINT IF EXISTS agent_task_role_check;

-- 2. 回填三角色为 agent（信息丢失：planner/exploitation/traffic-analysis 区分回不来，
--    重新跑 0050 up 会按 owner_type + parent_id 推回）
UPDATE agent_task SET role = 'agent'
    WHERE role IN ('traffic-analysis', 'planner', 'exploitation');

COMMENT ON COLUMN agent_task.role IS NULL;
