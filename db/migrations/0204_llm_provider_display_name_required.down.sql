DROP INDEX IF EXISTS uq_llm_provider_display_name;
ALTER TABLE llm_provider DROP CONSTRAINT IF EXISTS ck_llm_provider_display_name;
ALTER TABLE llm_provider ALTER COLUMN display_name SET DEFAULT '';
