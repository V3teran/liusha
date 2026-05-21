-- 0038: DDD 重构 — 新建 passive_session + active_scan，3 张共享表加 polymorphic owner（双轨）。
--
-- 设计动因：
--   - engagement 单表混了 2 种 mode 的字段（active.flow_count 永远 0、
--     passive.scope 永远 {"any":true}、active.expires_at 永远 NULL 等 dead 字段）
--   - finding.host 在 active 下回退 engagement_id 致 lesson 跨 engagement 复用静默失效
--   - active.brief 埋 jsonb 失去 SQL 可查询性
--
-- 本 migration 采用 **incremental 双轨**策略（非破坏式）：
--   1. 新建 passive_session + active_scan 表
--   2. agent_run / finding / llm_invocation 加 owner_type + owner_id（NULL，与 engagement_id 共存）
--   3. http_flow 加 passive_session_id（NULL，与 engagement_id 共存）
--   4. **保留** engagement 表 + 所有旧 engagement_id 列
--
-- 后续切换步骤（在新 commit 完成）：
--   B. caller 写路径改用新 store，写新表 + 新 owner 列
--   C. caller 读路径切到新 store
--   D. 数据回填 + DROP 旧列 + DROP TABLE engagement（在 0039+ migration）

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

-- 3. agent_run / finding / llm_invocation 加 polymorphic owner 列（与 engagement_id 双轨共存）
--    - owner_type / owner_id 都 NULL：表示走旧 engagement 路径
--    - owner_type / owner_id 都非 NULL：表示新路径（commit B 切换后写入）
ALTER TABLE agent_run ADD COLUMN owner_type text
    CHECK (owner_type IS NULL OR owner_type IN ('passive_session','active_scan'));
ALTER TABLE agent_run ADD COLUMN owner_id uuid;
CREATE INDEX agent_run_owner_idx ON agent_run (owner_type, owner_id, created_at DESC)
    WHERE owner_type IS NOT NULL;

ALTER TABLE finding ADD COLUMN owner_type text
    CHECK (owner_type IS NULL OR owner_type IN ('passive_session','active_scan'));
ALTER TABLE finding ADD COLUMN owner_id uuid;
CREATE INDEX finding_owner_idx ON finding (owner_type, owner_id, created_at DESC)
    WHERE owner_type IS NOT NULL;

ALTER TABLE llm_invocation ADD COLUMN owner_type text
    CHECK (owner_type IS NULL OR owner_type IN ('passive_session','active_scan'));
ALTER TABLE llm_invocation ADD COLUMN owner_id uuid;
CREATE INDEX llm_invocation_owner_idx ON llm_invocation (owner_type, owner_id)
    WHERE owner_type IS NOT NULL;

-- 4. http_flow 加 passive_session_id（与 engagement_id 双轨）
ALTER TABLE http_flow ADD COLUMN passive_session_id uuid
    REFERENCES passive_session(id) ON DELETE CASCADE;
CREATE INDEX http_flow_passive_session_idx ON http_flow (passive_session_id)
    WHERE passive_session_id IS NOT NULL;
