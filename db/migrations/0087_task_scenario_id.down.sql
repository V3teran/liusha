-- 回滚 0087：scenario_id → mode。
DELETE FROM task;
DROP INDEX IF EXISTS task_scenario_status_idx;
DROP INDEX IF EXISTS task_heartbeat_idx;
ALTER TABLE task DROP COLUMN scenario_id;
ALTER TABLE task ADD COLUMN mode text NOT NULL CHECK (mode IN ('active','passive'));
CREATE INDEX task_mode_status_idx ON task (mode, status, created_at DESC);
CREATE INDEX task_heartbeat_idx ON task (mode, heartbeat_at) WHERE status = 'active';
COMMENT ON TABLE task IS '统一扫描任务（合并 active_scan + passive_session）；mode 区分主动/被动';
