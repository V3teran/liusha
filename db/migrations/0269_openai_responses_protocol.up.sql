-- 接入 OpenAI Responses 协议（取值 openai_responses），它的输出上限字段只有 max_output_tokens 一个。
-- 取值的唯一出处在 framework/llm（ProviderTypes / OutputLimitParamOf）。
ALTER TABLE llm_provider DROP CONSTRAINT llm_provider_type_check;
ALTER TABLE llm_provider
    ADD CONSTRAINT llm_provider_type_check
    CHECK (type IN ('openai_chat_completions', 'openai_responses', 'anthropic_messages'));

ALTER TABLE llm_provider DROP CONSTRAINT ck_llm_provider_output_limit_param;
ALTER TABLE llm_provider
    ADD CONSTRAINT ck_llm_provider_output_limit_param
    CHECK (
        CASE type
            WHEN 'openai_responses' THEN output_limit_param = 'max_output_tokens'
            WHEN 'anthropic_messages' THEN output_limit_param = 'max_tokens'
            ELSE output_limit_param IN ('max_tokens', 'max_completion_tokens')
        END
    );
