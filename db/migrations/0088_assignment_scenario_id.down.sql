-- 回滚 0088：scenario_id → mode（assignment + cron_schedule）。
DELETE FROM task;
DELETE FROM assignment;
DELETE FROM cron_schedule;
ALTER TABLE assignment DROP COLUMN scenario_id;
ALTER TABLE assignment ADD COLUMN mode text NOT NULL CHECK (mode IN ('active','passive'));
ALTER TABLE cron_schedule DROP COLUMN scenario_id;
ALTER TABLE cron_schedule ADD COLUMN mode text NOT NULL CHECK (mode IN ('active','passive'));
