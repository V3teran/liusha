-- 回滚 0041：重建 engagement 表 + 加 engagement_id 列。
--
-- 注意：本 migration best-effort 反向；原 engagement 数据 + engagement_id 值无法恢复。
-- 仅复位 schema 结构让 0040 down 链可继续。

-- 1. 反向 owner NOT NULL 约束
ALTER TABLE finding ALTER COLUMN owner_type DROP NOT NULL;
ALTER TABLE finding ALTER COLUMN owner_id DROP NOT NULL;
ALTER TABLE agent_run ALTER COLUMN owner_type DROP NOT NULL;
ALTER TABLE agent_run ALTER COLUMN owner_id DROP NOT NULL;
ALTER TABLE llm_invocation ALTER COLUMN owner_type DROP NOT NULL;
ALTER TABLE llm_invocation ALTER COLUMN owner_id DROP NOT NULL;
ALTER TABLE http_flow ALTER COLUMN passive_session_id DROP NOT NULL;

-- 2. 重建 engagement 表（最低结构 + 唯一索引 + 过期索引）
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

-- 3. 加 engagement_id 列回 4 张共享表（nullable，无 FK——0040 仍是 DROP 状态）
ALTER TABLE finding ADD COLUMN engagement_id uuid;
ALTER TABLE agent_run ADD COLUMN engagement_id uuid;
ALTER TABLE llm_invocation ADD COLUMN engagement_id uuid;
ALTER TABLE http_flow ADD COLUMN engagement_id uuid;
