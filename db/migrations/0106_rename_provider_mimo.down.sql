-- 0106 down：把 mimo 还原回 xiaomi_mimo（0106 up 的逆，同样走插新→改引用→删旧，FK 安全）。
-- 幂等：仅当 mimo 存在且 xiaomi_mimo 不存在时才搬。

BEGIN;

INSERT INTO llm_provider (
  key, type, base_url, default_model, api_key_env, max_tokens,
  supports_tools, supports_vision, context_window, description, sort_order, enabled,
  created_at, updated_at, encrypted_api_key, api_key_last4
)
SELECT
  'xiaomi_mimo', type, base_url, default_model, api_key_env, max_tokens,
  supports_tools, supports_vision, context_window, description, sort_order, enabled,
  created_at, now(), encrypted_api_key, api_key_last4
FROM llm_provider
WHERE key = 'mimo'
  AND NOT EXISTS (SELECT 1 FROM llm_provider WHERE key = 'xiaomi_mimo')
ON CONFLICT (key) DO NOTHING;

UPDATE llm_role_route SET provider_key = 'xiaomi_mimo', updated_at = now()
WHERE provider_key = 'mimo';

DELETE FROM llm_provider WHERE key = 'mimo';

COMMIT;
