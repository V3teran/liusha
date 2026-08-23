BEGIN;

ALTER TABLE llm_provider DROP COLUMN api_key_last4;

COMMIT;
