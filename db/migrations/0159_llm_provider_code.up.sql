-- LLM provider 标识统一 code 寻址（与 agent.code / skill.code 同型）：
--   llm_provider.key → code；llm_role_route.provider_key → provider_code。
--   语义澄清：provider 的"标识"与"API 密钥"（api_key/key_present/key_last4）是两个概念，
--   旧列名 key 与密钥同域双义。web 契约键同步（ProviderConfig.code / RoleRouteConfig.provider_code）。

-- FK 为历史内联创建（约束名不可假设），动态摘除
DO $$
DECLARE r record;
BEGIN
    FOR r IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'llm_role_route'::regclass AND contype = 'f'
    LOOP
        EXECUTE format('ALTER TABLE llm_role_route DROP CONSTRAINT %I', r.conname);
    END LOOP;
END $$;

ALTER TABLE llm_provider RENAME COLUMN key TO code;
ALTER TABLE llm_role_route RENAME COLUMN provider_key TO provider_code;

ALTER TABLE llm_role_route
  ADD CONSTRAINT llm_role_route_provider_code_fkey
  FOREIGN KEY (provider_code) REFERENCES llm_provider(code) ON DELETE RESTRICT;
