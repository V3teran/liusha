-- 0003_llm_payload.up.sql
-- 为 llm_call 表新增完整 I/O 落库列：messages_json（输入消息数组）+ result_json（LLM 返回）。
-- 设计：
--   - 始终落库（不带开关），便于审计/回放/调试。
--   - jsonb 类型；NOT NULL DEFAULT 防止历史行 SELECT 失败。
--   - 不脱敏：cookie/auth header 原样保留（仅内网/开发环境，prod 部署需结合 RBAC + 加密磁盘）。

ALTER TABLE llm_call
    ADD COLUMN messages_json jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN result_json   jsonb NOT NULL DEFAULT '{}'::jsonb;
