-- 回滚 provider 请求画像：鉴权方式回到协议标准方式、输出上限字段回到 max_tokens。
ALTER TABLE llm_provider DROP COLUMN output_limit_param;
ALTER TABLE llm_provider DROP COLUMN auth_scheme;
