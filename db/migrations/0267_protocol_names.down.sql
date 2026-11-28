ALTER TABLE llm_provider DROP CONSTRAINT llm_provider_type_check;

UPDATE llm_provider SET type = 'anthropic' WHERE type = 'anthropic_messages';
UPDATE llm_provider SET type = 'openai_compat' WHERE type = 'openai_chat_completions';

ALTER TABLE llm_provider
    ADD CONSTRAINT llm_provider_type_check
    CHECK (type IN ('openai_compat', 'anthropic'));
