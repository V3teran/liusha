CREATE INDEX IF NOT EXISTS llm_invocation_task_idx ON llm_invocation (task_id);
DROP INDEX IF EXISTS llm_invocation_task_id_idx;
