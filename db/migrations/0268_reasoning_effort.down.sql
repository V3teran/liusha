ALTER TABLE llm_provider DROP CONSTRAINT ck_llm_provider_reasoning_effort;
ALTER TABLE llm_provider DROP COLUMN reasoning_effort;
ALTER TABLE llm_provider ADD COLUMN thinking_budget_tokens bigint NOT NULL DEFAULT 0;
