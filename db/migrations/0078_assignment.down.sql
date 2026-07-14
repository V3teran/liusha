-- 0078 down: 回滚 assignment / cron_schedule / task.assignment_id。
-- 清库前提，不搬运数据。顺序与 up 相反：先去 task 外键列，再删两表。

DROP INDEX IF EXISTS task_assignment_idx;
ALTER TABLE task DROP COLUMN IF EXISTS assignment_id;

DROP TABLE IF EXISTS assignment;
DROP TABLE IF EXISTS cron_schedule;
