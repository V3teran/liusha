-- 协议取值改为「厂商 + 接口名」：同一家有多种协议时，只写厂商（anthropic）或笼统的
-- 「OpenAI 兼容」（openai_compat）分不清是哪个接口。取值的唯一出处在 framework/llm
-- （ProviderTypes），库表 CHECK、HTTP 契约与前端都以它为准。
ALTER TABLE llm_provider DROP CONSTRAINT llm_provider_type_check;

UPDATE llm_provider SET type = 'openai_chat_completions' WHERE type = 'openai_compat';
UPDATE llm_provider SET type = 'anthropic_messages' WHERE type = 'anthropic';

ALTER TABLE llm_provider
    ADD CONSTRAINT llm_provider_type_check
    CHECK (type IN ('openai_chat_completions', 'anthropic_messages'));
