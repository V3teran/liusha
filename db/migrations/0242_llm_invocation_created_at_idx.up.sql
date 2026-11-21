-- LLM 调用按时间范围读写：概览按时间窗统计每天的消耗与最近 24 小时的调用，保留期清扫按
-- created_at 删除过期记录。调用记录随每次 LLM 调用增长，没有这个索引两者都要全表扫描。
CREATE INDEX IF NOT EXISTS llm_invocation_created_at_idx ON llm_invocation (created_at);
