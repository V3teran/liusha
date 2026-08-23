-- 0074 down: task_id 单列 → 多态 owner（owner_type + owner_id）。
-- 清库前提，不回填数据；恢复列结构与 0038/0046 一致的多态形态。

-- ── tool_invocation
DROP INDEX IF EXISTS tool_invocation_task_idx;
ALTER TABLE tool_invocation DROP COLUMN IF EXISTS task_id;
ALTER TABLE tool_invocation ADD COLUMN owner_type text NOT NULL DEFAULT 'active_scan'
    CHECK (owner_type IN ('passive_session','active_scan'));
ALTER TABLE tool_invocation ADD COLUMN owner_id uuid NOT NULL DEFAULT gen_random_uuid();
CREATE INDEX tool_invocation_owner_idx ON tool_invocation (owner_type, owner_id, created_at DESC);

-- ── llm_invocation
DROP INDEX IF EXISTS llm_invocation_task_idx;
ALTER TABLE llm_invocation DROP COLUMN IF EXISTS task_id;
ALTER TABLE llm_invocation ADD COLUMN owner_type text
    CHECK (owner_type IS NULL OR owner_type IN ('passive_session','active_scan'));
ALTER TABLE llm_invocation ADD COLUMN owner_id uuid;
CREATE INDEX llm_invocation_owner_idx ON llm_invocation (owner_type, owner_id)
    WHERE owner_type IS NOT NULL;

-- ── finding
DROP INDEX IF EXISTS finding_task_dedup_uniq;
DROP INDEX IF EXISTS finding_task_idx;
ALTER TABLE finding DROP COLUMN IF EXISTS task_id;
ALTER TABLE finding ADD COLUMN owner_type text
    CHECK (owner_type IS NULL OR owner_type IN ('passive_session','active_scan'));
ALTER TABLE finding ADD COLUMN owner_id uuid;
CREATE INDEX finding_owner_idx ON finding (owner_type, owner_id, created_at DESC)
    WHERE owner_type IS NOT NULL;
CREATE UNIQUE INDEX finding_owner_dedup_uniq ON finding (owner_id, dedup_key);

-- ── agent
DROP INDEX IF EXISTS agent_task_idx;
ALTER TABLE agent DROP COLUMN IF EXISTS task_id;
ALTER TABLE agent ADD COLUMN owner_type text
    CHECK (owner_type IS NULL OR owner_type IN ('passive_session','active_scan'));
ALTER TABLE agent ADD COLUMN owner_id uuid;
CREATE INDEX agent_run_owner_idx ON agent (owner_type, owner_id, created_at DESC)
    WHERE owner_type IS NOT NULL;
