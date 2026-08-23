-- 0106: provider key 从 xiaomi_mimo 重命名为 mimo（遵循官方叫法「小米 MiMo」，key 用简洁 mimo）。
--
-- 动机（用户裁决）：xiaomi_mimo 是内部自造 key，冗长且非官方称呼。统一为 mimo。
--
-- 为何不能就地 UPDATE key：llm_role_route.provider_key 有 FK REFERENCES llm_provider(key)
-- ON DELETE RESTRICT（且无 ON UPDATE CASCADE，即 NO ACTION）——直接改父表 PK 会因残留引用行报错。
-- 故走「插新行 → 改引用 → 删旧行」三步，FK 全程满足。
--
-- 幂等：仅当 xiaomi_mimo 存在且 mimo 不存在时才搬（空库/已改过均 noop）。

BEGIN;

-- 1) 复制 xiaomi_mimo 为 mimo（全列，含加密密钥/尾号，保留原凭据）。
INSERT INTO llm_provider (
  key, type, base_url, default_model, api_key_env, max_tokens,
  supports_tools, supports_vision, context_window, description, sort_order, enabled,
  created_at, updated_at, encrypted_api_key, api_key_last4
)
SELECT
  'mimo', type, base_url, default_model, api_key_env, max_tokens,
  supports_tools, supports_vision, context_window, description, sort_order, enabled,
  created_at, now(), encrypted_api_key, api_key_last4
FROM llm_provider
WHERE key = 'xiaomi_mimo'
  AND NOT EXISTS (SELECT 1 FROM llm_provider WHERE key = 'mimo')
ON CONFLICT (key) DO NOTHING;

-- 2) 把所有指向 xiaomi_mimo 的路由改指 mimo。
UPDATE llm_role_route SET provider_key = 'mimo', updated_at = now()
WHERE provider_key = 'xiaomi_mimo';

-- 3) 删旧 provider 行（此时已无引用，FK RESTRICT 满足）。
DELETE FROM llm_provider WHERE key = 'xiaomi_mimo';

COMMIT;
