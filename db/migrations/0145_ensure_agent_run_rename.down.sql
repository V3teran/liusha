-- 回滚重命名
ALTER TABLE agent_run RENAME TO agent_task;

DROP INDEX IF EXISTS idx_agent_run_created_at;
DROP INDEX IF EXISTS idx_agent_run_task_id;
DROP INDEX IF EXISTS idx_agent_run_status;
