-- 0037 down: 回滚 parent_id 列。
DROP INDEX IF EXISTS agent_run_parent_id_idx;
ALTER TABLE agent_run DROP COLUMN IF EXISTS parent_id;
