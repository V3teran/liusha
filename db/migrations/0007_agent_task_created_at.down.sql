DROP INDEX IF EXISTS agent_task_created_at_idx;
ALTER TABLE agent_task DROP COLUMN IF EXISTS created_at;
