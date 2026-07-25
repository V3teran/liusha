DROP INDEX IF EXISTS llm_invocation_request_id_idx;
ALTER TABLE llm_invocation DROP COLUMN IF EXISTS request_id;
