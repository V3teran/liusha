-- 0069 回滚：复原 llm_invocation.cost_usd 列（历史 cost 数据已永久丢失，仅恢复 schema）。
ALTER TABLE llm_invocation ADD COLUMN IF NOT EXISTS cost_usd numeric(12,6) NOT NULL DEFAULT 0;
