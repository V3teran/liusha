ALTER TABLE llm_provider DROP CONSTRAINT ck_llm_provider_output_limit_param;
ALTER TABLE llm_provider
    ADD CONSTRAINT ck_llm_provider_output_limit_param
    CHECK (output_limit_param IN ('max_tokens', 'max_completion_tokens'));

ALTER TABLE llm_provider DROP CONSTRAINT llm_provider_type_check;
ALTER TABLE llm_provider
    ADD CONSTRAINT llm_provider_type_check
    CHECK (type IN ('openai_chat_completions', 'anthropic_messages'));

-- 已按 Responses 配置的 provider 没有可退回去的等价写法：转成 Chat Completions（地址相同，
-- 输出上限字段按多数服务认的 max_tokens），由部署方核对。
UPDATE llm_provider SET type = 'openai_chat_completions', output_limit_param = 'max_tokens' WHERE type = 'openai_responses';
