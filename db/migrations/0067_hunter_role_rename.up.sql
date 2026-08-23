-- 0067: agent.role 三角色重命名（命名统一，PTES 业界术语；react/subtask 退路已删）
--   traffic-analysis   → traffic-analysis （passive 单 agent）
--   planner → planner     （active 主代理）
--   exploitation   → exploitation     （active 子代理；deep 已不建独立 agent，仅历史数据）
-- 先 backfill 旧行，再换 CHECK 约束。

ALTER TABLE agent DROP CONSTRAINT agent_role_check;

UPDATE agent SET role = 'planner' WHERE role = 'planner';
UPDATE agent SET role = 'exploitation' WHERE role = 'exploitation';
UPDATE agent SET role = 'traffic-analysis' WHERE role = 'traffic-analysis';

ALTER TABLE agent ADD CONSTRAINT agent_role_check
	CHECK (role = ANY (ARRAY['traffic-analysis'::text, 'planner'::text, 'exploitation'::text]));
