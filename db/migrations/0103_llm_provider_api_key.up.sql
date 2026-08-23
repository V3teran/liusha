-- 0103: provider API Key 改前端直填、后端加密持久化——取代原「只存 ENV 变量名，值只在 .env」模式。
--
-- 动机（用户裁决）：运维现在得手改 .env + 重启进程才能换/加一个 provider 的密钥，
-- 前端「LLM 配置」页管不了这一步。改成前端直填明文（传输层已有 X-API-Key + HTTPS 保护），
-- 后端用 AES-256-GCM 加密后落库，密文才进 DB/Redis 缓存，解密只发生在构造 LLM client 那一刻
-- （internal/llm/factory.go、internal/einollm/factory.go），不进任何缓存层。
--
-- 保留 api_key_env 列（改为可空）：兼容尚未通过前端重新保存密钥的旧 provider 行——
-- 应用层解密时 encrypted_api_key 为空则回退读 os.Getenv(api_key_env)（见 internal/llm 层），
-- 避免这条迁移本身就让所有现有 provider 瞬间失效。

BEGIN;

ALTER TABLE llm_provider ADD COLUMN encrypted_api_key bytea;
ALTER TABLE llm_provider ALTER COLUMN api_key_env DROP NOT NULL;

COMMENT ON COLUMN llm_provider.encrypted_api_key IS 'AES-256-GCM 密文（nonce||ciphertext），密钥来自 LIUSHA_LLM_KEY_SECRET；为空时回退 api_key_env 环境变量（旧数据兼容）';
COMMENT ON COLUMN llm_provider.api_key_env IS '旧模式：环境变量名（仅当 encrypted_api_key 为空时生效的回退路径）';

COMMIT;
