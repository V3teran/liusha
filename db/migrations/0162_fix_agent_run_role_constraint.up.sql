-- 修复 agent_run.role 约束，对齐四 agent 架构（planner|executor|evaluator|monitor）
--
-- 问题：当前约束允许 traffic-analysis|planner|exploitation（旧架构三角色），
-- 但实际 agent 表只有 planner|executor|evaluator|monitor（新架构四角色）。
--
-- 修复：统一为新架构的四角色，迁移历史数据。

-- 1. 删除旧约束
ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_role_check;
ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_task_role_check;

-- 2. 迁移历史数据（如果有旧角色名）
UPDATE agent_run SET role = 'executor' WHERE role = 'exploitation';
UPDATE agent_run SET role = 'monitor' WHERE role = 'traffic-analysis';

-- 3. 添加新约束（对齐 agent.code 的四个枚举值）
ALTER TABLE agent_run ADD CONSTRAINT agent_run_role_check
  CHECK (role = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text]));

-- 4. 验证数据完整性
DO $$
DECLARE
  invalid_count int;
  invalid_roles text;
BEGIN
  SELECT COUNT(*), STRING_AGG(DISTINCT role, ', ')
  INTO invalid_count, invalid_roles
  FROM agent_run
  WHERE role NOT IN ('planner', 'executor', 'evaluator', 'monitor');

  IF invalid_count > 0 THEN
    RAISE EXCEPTION '发现 % 条 agent_run 记录的 role 值不合法: %', invalid_count, invalid_roles;
  END IF;

  RAISE NOTICE '✓ agent_run.role 约束已修复，所有数据合法';
END $$;
