-- 0171 down: 恢复 llm_invocation 的 ttft_ms / is_stream 列（历史值不可恢复，恒为默认值）。

BEGIN;

ALTER TABLE llm_invocation
    ADD COLUMN ttft_ms   integer NOT NULL DEFAULT 0,
    ADD COLUMN is_stream boolean NOT NULL DEFAULT false;

COMMIT;
