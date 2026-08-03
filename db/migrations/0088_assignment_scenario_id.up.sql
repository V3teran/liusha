-- 0088: assignment + cron_schedule 的 mode → scenario_id。
-- source 列保持不变（manual/auto，审计用，见 D3）。
DELETE FROM task;        -- FK CASCADE 会连带；先清子表再改父
DELETE FROM assignment;
DELETE FROM cron_schedule;

ALTER TABLE assignment DROP COLUMN mode;
ALTER TABLE assignment ADD COLUMN scenario_id text NOT NULL;

ALTER TABLE cron_schedule DROP COLUMN mode;
ALTER TABLE cron_schedule ADD COLUMN scenario_id text NOT NULL;

COMMENT ON COLUMN assignment.scenario_id IS '本次下发所属场景 id（裸 text，应用层校验）';
COMMENT ON COLUMN cron_schedule.scenario_id IS '定时触发时下发的场景 id（裸 text，应用层校验）';
