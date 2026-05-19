-- 0038: DDD 重构 — 拆 engagement 为 passive_session + active_scan + 5 张共享表改 polymorphic owner。
--
-- 设计动因：
--   - engagement 单表混了 2 种 mode 的字段（active.flow_count 永远 0、
--     passive.scope 永远 {"any":true}、active.expires_at 永远 NULL 等 dead 字段）
--   - finding.host 在 active 下回退 engagement_id 致 lesson 跨 engagement 复用静默失效
--   - active.brief 埋 jsonb 失去 SQL 可查询性
--
-- 本 migration 破坏式（用户授权清空数据）：
--   1. 新建 passive_session + active_scan（替代 engagement）
--   2. agent_run / finding / llm_invocation 改 polymorphic owner（owner_type + owner_id 二字段）
--   3. http_flow.engagement_id 改名 passive_session_id（active 不入此表）
--   4. DROP TABLE engagement
--
-- 共享表（不变）：lesson、finding_relation——本来就跨模式通用。

-- 1. passive_session：流量驱动的持续监控会话（host 是核心实体）
CREATE TABLE passive_session (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host            text NOT NULL,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','archived','aborted')),
    expires_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    error_message   text,
    flow_count      int NOT NULL DEFAULT 0,
    finding_count   int NOT NULL DEFAULT 0,
    agent_run_count int NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX passive_session_active_host_uniq
    ON passive_session (host) WHERE status = 'active';
CREATE INDEX passive_session_status_idx ON passive_session (status, created_at DESC);

-- 2. active_scan：任务驱动的一次性扫描（brief 是核心实体）
CREATE TABLE active_scan (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    brief           text NOT NULL,
    target_host     text,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','archived','aborted')),
    ended_at        timestamptz,
    error_message   text,
    finding_count   int NOT NULL DEFAULT 0,
    agent_run_count int NOT NULL DEFAULT 0,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX active_scan_status_idx ON active_scan (status, created_at DESC);

-- 3. agent_run / finding / llm_invocation 改 polymorphic owner
ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_engagement_id_fkey;
ALTER TABLE agent_run DROP COLUMN engagement_id;
ALTER TABLE agent_run ADD COLUMN owner_type text NOT NULL
    CHECK (owner_type IN ('passive_session','active_scan'));
ALTER TABLE agent_run ADD COLUMN owner_id uuid NOT NULL;
CREATE INDEX agent_run_owner_idx ON agent_run (owner_type, owner_id, created_at DESC);

ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_engagement_id_fkey;
ALTER TABLE finding DROP COLUMN engagement_id;
ALTER TABLE finding ADD COLUMN owner_type text NOT NULL
    CHECK (owner_type IN ('passive_session','active_scan'));
ALTER TABLE finding ADD COLUMN owner_id uuid NOT NULL;
CREATE INDEX finding_owner_idx ON finding (owner_type, owner_id, created_at DESC);

ALTER TABLE llm_invocation DROP CONSTRAINT IF EXISTS llm_invocation_engagement_id_fkey;
ALTER TABLE llm_invocation DROP COLUMN engagement_id;
ALTER TABLE llm_invocation ADD COLUMN owner_type text NOT NULL
    CHECK (owner_type IN ('passive_session','active_scan'));
ALTER TABLE llm_invocation ADD COLUMN owner_id uuid NOT NULL;
CREATE INDEX llm_invocation_owner_idx ON llm_invocation (owner_type, owner_id);

-- 4. http_flow 改用 passive_session_id（active 不入此表，所以 FK 直接到 passive_session）
ALTER TABLE http_flow DROP CONSTRAINT IF EXISTS http_flow_engagement_id_fkey;
ALTER TABLE http_flow RENAME COLUMN engagement_id TO passive_session_id;
ALTER TABLE http_flow ADD CONSTRAINT http_flow_session_fkey
    FOREIGN KEY (passive_session_id) REFERENCES passive_session(id) ON DELETE CASCADE;

-- 5. 删旧 engagement 表（破坏式，用户授权清空数据）
DROP TABLE engagement;
