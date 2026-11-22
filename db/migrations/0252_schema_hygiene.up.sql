-- 零散的冗余与缺失：
--
-- 1. account_session.renewed_at 只在续期时写入，没有任何读取方（会话的有效期是 expires_at）。
-- 2. audit_log 上的三个组合索引没有任何查询使用：审计日志目前只写不读；将来提供查看入口时，
--    按它的查询形态建索引。
-- 3. tool_invocation (tool_name, created_at) 没有查询使用：工具调用只按任务合计。
-- 4. llm_role_route.role 是三个复杂度档之一（llmconfig.IsComplexity），库里补上约束。
-- 5. pgcrypto 扩展没有任何地方使用（UUID 默认值是内置的 gen_random_uuid，密钥加密在应用层）。
ALTER TABLE account_session DROP COLUMN renewed_at;

DROP INDEX IF EXISTS audit_log_action_idx;
DROP INDEX IF EXISTS audit_log_actor_idx;
DROP INDEX IF EXISTS audit_log_target_idx;

DROP INDEX IF EXISTS tool_invocation_name_idx;

ALTER TABLE llm_role_route ADD CONSTRAINT llm_role_route_role_check
    CHECK (role IN ('simple', 'medium', 'complex'));

DROP EXTENSION IF EXISTS pgcrypto;
