-- 0067: hunter.role 三角色重命名（命名统一，PTES 业界术语；react/subtask 退路已删）
--   tracker   → traffic-analysis （passive 单 agent）
--   commander → orchestrator     （active 主代理）
--   striker   → exploitation     （active 子代理；deep 已不建独立 hunter，仅历史数据）
-- 先 backfill 旧行，再换 CHECK 约束。

ALTER TABLE hunter DROP CONSTRAINT hunter_role_check;

UPDATE hunter SET role = 'orchestrator' WHERE role = 'commander';
UPDATE hunter SET role = 'exploitation' WHERE role = 'striker';
UPDATE hunter SET role = 'traffic-analysis' WHERE role = 'tracker';

ALTER TABLE hunter ADD CONSTRAINT hunter_role_check
	CHECK (role = ANY (ARRAY['traffic-analysis'::text, 'orchestrator'::text, 'exploitation'::text]));
