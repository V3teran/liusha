-- 回滚 agent_run.role 约束修复

ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_role_check;

-- 恢复旧约束（三角色）
ALTER TABLE agent_run ADD CONSTRAINT agent_run_role_check
  CHECK (role = ANY (ARRAY['traffic-analysis'::text, 'planner'::text, 'exploitation'::text]));

-- 注意：数据迁移不回滚（executor/monitor 会违反旧约束）
-- 如需完整回滚，需要手动将数据改回旧角色名
