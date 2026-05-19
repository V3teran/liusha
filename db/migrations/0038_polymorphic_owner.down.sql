-- 回滚 0038：恢复 engagement 单表 + 反向 polymorphic owner。
--
-- **破坏式回滚**：数据不可恢复（原 passive_session/active_scan 的数据无法回填到 engagement，
-- 因为 polymorphic owner 数据已切换到新表）。仅恢复 schema 结构，让 migrate down 链可继续。

-- 1. http_flow.passive_session_id 改回 engagement_id（但 FK 暂不加，因 engagement 表此刻还不存在）
ALTER TABLE http_flow DROP CONSTRAINT IF EXISTS http_flow_session_fkey;
ALTER TABLE http_flow RENAME COLUMN passive_session_id TO engagement_id;

-- 2. 共享表反向：DROP owner_type/owner_id + ADD engagement_id
DROP INDEX IF EXISTS agent_run_owner_idx;
ALTER TABLE agent_run DROP COLUMN owner_type;
ALTER TABLE agent_run DROP COLUMN owner_id;
ALTER TABLE agent_run ADD COLUMN engagement_id uuid;

DROP INDEX IF EXISTS finding_owner_idx;
ALTER TABLE finding DROP COLUMN owner_type;
ALTER TABLE finding DROP COLUMN owner_id;
ALTER TABLE finding ADD COLUMN engagement_id uuid;

DROP INDEX IF EXISTS llm_invocation_owner_idx;
ALTER TABLE llm_invocation DROP COLUMN owner_type;
ALTER TABLE llm_invocation DROP COLUMN owner_id;
ALTER TABLE llm_invocation ADD COLUMN engagement_id uuid;

-- 3. DROP 新增 2 表
DROP INDEX IF EXISTS passive_session_active_host_uniq;
DROP INDEX IF EXISTS passive_session_status_idx;
DROP TABLE passive_session;

DROP INDEX IF EXISTS active_scan_status_idx;
DROP TABLE active_scan;

-- 4. 重建 engagement 表（最低结构，覆盖 0033/0035/0036 之后的核心字段）
--    不重建早期已删字段（target_host、memory_*、tenant_id、notes 等）——0001~0032 down 链不会再走到这。
CREATE TABLE engagement (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode            text NOT NULL CHECK (mode IN ('passive','active')),
    scope           jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL CHECK (status IN ('active','aborted','archived')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz,
    ended_at        timestamptz,
    error_message   text,
    flow_count      int NOT NULL DEFAULT 0,
    finding_count   int NOT NULL DEFAULT 0,
    agent_run_count int NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX engagement_active_passive_uniq
  ON engagement ((1)) WHERE status = 'active' AND mode = 'passive';
CREATE INDEX engagement_expires_idx ON engagement (expires_at)
  WHERE status = 'active' AND expires_at IS NOT NULL;

-- 5. 重加 FK（此刻 engagement 表已存在，旧 engagement_id 列 NULL 不约束）
ALTER TABLE http_flow ADD CONSTRAINT http_flow_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
ALTER TABLE agent_run ADD CONSTRAINT agent_run_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
ALTER TABLE finding ADD CONSTRAINT finding_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
ALTER TABLE llm_invocation ADD CONSTRAINT llm_invocation_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
