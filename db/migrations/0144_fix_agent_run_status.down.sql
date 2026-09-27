-- 回滚 agent_run 表的修复
DROP INDEX IF EXISTS idx_agent_run_task_id;
DROP INDEX IF EXISTS idx_agent_run_status;

ALTER TABLE agent_run DROP COLUMN IF EXISTS status;
ALTER TABLE agent_run DROP COLUMN IF EXISTS result;
