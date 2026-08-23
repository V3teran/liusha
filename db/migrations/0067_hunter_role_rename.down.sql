-- 回滚 0067：role 值与 CHECK 约束改回 traffic-analysis/planner/exploitation。

ALTER TABLE agent DROP CONSTRAINT agent_role_check;

UPDATE agent SET role = 'planner' WHERE role = 'planner';
UPDATE agent SET role = 'exploitation' WHERE role = 'exploitation';
UPDATE agent SET role = 'traffic-analysis' WHERE role = 'traffic-analysis';

ALTER TABLE agent ADD CONSTRAINT agent_role_check
	CHECK (role = ANY (ARRAY['traffic-analysis'::text, 'planner'::text, 'exploitation'::text]));
