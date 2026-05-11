-- 0028 down: 回滚命名清理 + summary check

-- 3. 撤 finding.summary check
ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_summary_max_500;
ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_summary_one_line;

-- 2. 回滚 llm_invocation 命名
ALTER TABLE    llm_invocation
    RENAME CONSTRAINT llm_invocation_engagement_id_fkey TO llm_call_engagement_id_fkey;
ALTER INDEX    llm_invocation_engagement_idx RENAME TO llm_call_engagement_idx;
ALTER INDEX    llm_invocation_pkey   RENAME TO llm_call_pkey;
ALTER SEQUENCE llm_invocation_id_seq RENAME TO llm_call_id_seq;

-- 1. 恢复 agent_run.skill（默认空字符串）
ALTER TABLE agent_run ADD COLUMN skill text NOT NULL DEFAULT '';
