ALTER TABLE public.llm_provider ADD COLUMN api_key_env text;

COMMENT ON COLUMN public.llm_provider.api_key_env IS '部署期密钥路径：环境变量名（encrypted_api_key 为空时生效）';
COMMENT ON COLUMN public.llm_provider.encrypted_api_key IS 'AES-256-GCM 密文（nonce||ciphertext），密钥来自 LIUSHA_LLM_KEY_SECRET；非空时优先于 api_key_env';
