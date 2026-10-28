-- 0181 down: 移除批次描述列。

ALTER TABLE cron_schedule DROP COLUMN IF EXISTS description;
ALTER TABLE assignment    DROP COLUMN IF EXISTS description;
