-- provider 的请求画像：协议之外，各服务在两处细节上不统一，由连接诊断实测后记录。
-- 1. auth_scheme：密钥的携带方式（bearer = Authorization: Bearer；x-api-key = x-api-key 头）。
--    与协议正交——同一协议的官方端点、兼容端点、中转网关可能只认其中一种。
--    存量行按协议标准方式回填（openai_compat → bearer，anthropic → x-api-key）。
-- 2. output_limit_param：OpenAI 兼容协议声明输出上限的字段（max_tokens 或
--    max_completion_tokens）；anthropic 协议恒为 max_tokens。
ALTER TABLE llm_provider ADD COLUMN auth_scheme text;
UPDATE llm_provider SET auth_scheme = CASE WHEN type = 'anthropic' THEN 'x-api-key' ELSE 'bearer' END;
ALTER TABLE llm_provider ALTER COLUMN auth_scheme SET NOT NULL;
ALTER TABLE llm_provider
    ADD CONSTRAINT ck_llm_provider_auth_scheme CHECK (auth_scheme IN ('bearer', 'x-api-key'));

ALTER TABLE llm_provider ADD COLUMN output_limit_param text NOT NULL DEFAULT 'max_tokens';
ALTER TABLE llm_provider
    ADD CONSTRAINT ck_llm_provider_output_limit_param
        CHECK (output_limit_param IN ('max_tokens', 'max_completion_tokens'));
