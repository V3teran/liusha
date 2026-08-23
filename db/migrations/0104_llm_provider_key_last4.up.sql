-- 0104: provider API Key 脱敏尾号——存明文末 4 位，供前端列表/编辑态辨识「这是哪把钥」。
--
-- 动机：0103 把密钥改为加密落库、GET 永不回显明文。但前端只有 key_present 布尔，
-- 同一 provider 换过几把钥、或有多个 deepseek 部署时，用户分不清当前用的是哪把。
-- 业界（OpenAI/Stripe/GitHub PAT）通行做法：列表只展示脱敏尾号（sk-…4f2a），
-- 4 位明文尾号不构成泄密，却给足辨识锚点。保存新密钥时后端从明文取末 4 位写入本列。
--
-- 可空：0103 之前的旧行、以及仅走 api_key_env 回退路径的 provider 没有 last4，留空即可
--（前端此时回落展示「已配置」而不带尾号）。

BEGIN;

ALTER TABLE llm_provider ADD COLUMN api_key_last4 text;

COMMENT ON COLUMN llm_provider.api_key_last4 IS 'API Key 明文末 4 位（脱敏辨识用，非密钥值本身）；为空表示未知（旧行或仅 ENV 回退）';

COMMIT;
