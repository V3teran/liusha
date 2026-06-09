-- 回滚 0067：role 值与 CHECK 约束改回 tracker/commander/striker。

ALTER TABLE hunter DROP CONSTRAINT hunter_role_check;

UPDATE hunter SET role = 'commander' WHERE role = 'orchestrator';
UPDATE hunter SET role = 'striker' WHERE role = 'exploitation';
UPDATE hunter SET role = 'tracker' WHERE role = 'traffic-analysis';

ALTER TABLE hunter ADD CONSTRAINT hunter_role_check
	CHECK (role = ANY (ARRAY['tracker'::text, 'commander'::text, 'striker'::text]));
