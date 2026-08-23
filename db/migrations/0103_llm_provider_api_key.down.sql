BEGIN;

ALTER TABLE llm_provider ALTER COLUMN api_key_env SET NOT NULL;
ALTER TABLE llm_provider DROP COLUMN encrypted_api_key;

COMMIT;
