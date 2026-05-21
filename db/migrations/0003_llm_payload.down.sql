-- 0003_llm_payload.down.sql
-- 回滚 llm_call 上的 I/O 落库列。

ALTER TABLE llm_call
    DROP COLUMN IF EXISTS result_json,
    DROP COLUMN IF EXISTS messages_json;
