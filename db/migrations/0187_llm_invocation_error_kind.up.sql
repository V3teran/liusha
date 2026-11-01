-- llm_invocation 增加错误分类列：把限流/网络/服务端等瞬时失败从原始错误文本
-- 中提升为一等枚举，审计面可按类聚合与徽标化（如 429 限流在审计列表一眼可辨，
-- 而不是一行空摘要 + 一段 URL 错误串）。分类在写入时由应用层判定（单一事实源），
-- 库层只存结果。历史行保持空串 = 未分类（错误文本仍在 error_message 可读）。
ALTER TABLE llm_invocation ADD COLUMN error_kind text NOT NULL DEFAULT '';
COMMENT ON COLUMN llm_invocation.error_kind IS '错误分类：rate_limited|network|server|error（空=成功或未分类）；应用层按框架错误分类判定';
