-- 回到删列之前（以新值回填）。
ALTER TABLE llm_invocation ADD COLUMN request_id text NOT NULL DEFAULT (gen_random_uuid())::text;
CREATE INDEX llm_invocation_request_id_idx ON llm_invocation (request_id);
