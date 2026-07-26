ALTER TABLE llm_invocation
    DROP COLUMN IF EXISTS ttft_ms,
    DROP COLUMN IF EXISTS is_stream;
