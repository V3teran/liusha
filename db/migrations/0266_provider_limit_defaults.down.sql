ALTER TABLE llm_provider ALTER COLUMN context_window DROP DEFAULT;
ALTER TABLE llm_provider ALTER COLUMN max_tokens SET DEFAULT 4096;
