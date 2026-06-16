-- 回滚 0067：role 值与 CHECK 约束改回 traffic-analysis/orchestrator/exploitation。

ALTER TABLE hunter DROP CONSTRAINT hunter_role_check;

UPDATE hunter SET role = 'orchestrator' WHERE role = 'orchestrator';
UPDATE hunter SET role = 'exploitation' WHERE role = 'exploitation';
UPDATE hunter SET role = 'traffic-analysis' WHERE role = 'traffic-analysis';

ALTER TABLE hunter ADD CONSTRAINT hunter_role_check
	CHECK (role = ANY (ARRAY['traffic-analysis'::text, 'orchestrator'::text, 'exploitation'::text]));
