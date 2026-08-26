-- Migration 0121 Down: 回滚到分离的 Move 和 Node

-- =====================================================================
-- 第 1 步：重建 planned_move 表
-- =====================================================================
CREATE TABLE planned_move (
    id          UUID PRIMARY KEY,
    task_id     TEXT NOT NULL,
    kind        TEXT NOT NULL,
    domain      TEXT NOT NULL,
    target_ref  JSONB NOT NULL,
    priority    INT NOT NULL DEFAULT 0,
    status      TEXT NOT NULL,
    depends_on  UUID[] DEFAULT '{}',
    reason      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at  TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_message TEXT,

    CONSTRAINT planned_move_domain_check CHECK (domain IN ('web', 'binary', 'cloud', 'lateral', 'network')),
    CONSTRAINT planned_move_kind_check CHECK (kind IN ('enumerate', 'probe', 'exploit', 'escalate', 'persist')),
    CONSTRAINT planned_move_status_check CHECK (status IN ('pending', 'executing', 'completed', 'failed'))
);

CREATE INDEX idx_planned_move_task_status ON planned_move(task_id, status);
CREATE INDEX idx_planned_move_task_priority ON planned_move(task_id, priority DESC);

-- =====================================================================
-- 第 2 步：重建旧 wm_node 表
-- =====================================================================
CREATE TABLE wm_node_old (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('target', 'asset', 'credential', 'access', 'finding')),
    ref        JSONB NOT NULL,
    attrs      JSONB NOT NULL DEFAULT '{}',
    confidence TEXT CHECK (confidence IN ('assumed', 'confirmed')),
    verified_by TEXT,
    seq        BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_wm_node_old_task_kind ON wm_node_old(task_id, kind);

-- =====================================================================
-- 第 3 步：重建旧 wm_edge 表
-- =====================================================================
CREATE TABLE wm_edge_old (
    id        TEXT PRIMARY KEY,
    task_id   TEXT NOT NULL,
    rel       TEXT NOT NULL CHECK (rel IN ('derives', 'enables', 'on', 'spawns', 'produces', 'promotes', 'refutes')),
    src       TEXT NOT NULL,
    dst       TEXT NOT NULL,
    attrs     JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- 第 4 步：从统一 wm_node 迁移回 planned_move
-- =====================================================================
INSERT INTO planned_move (
    id, task_id, kind, domain, target_ref, priority, status, depends_on, reason, created_at, completed_at
)
SELECT
    id::UUID,
    task_id,
    'enumerate',  -- 简化映射
    (content->>'target_ref'->>'domain')::TEXT,
    (content->'target_ref')::JSONB,
    priority,
    CASE state
        WHEN 'open' THEN 'pending'
        WHEN 'running' THEN 'executing'
        WHEN 'done' THEN 'completed'
        WHEN 'failed' THEN 'failed'
        ELSE 'pending'
    END,
    depends_on,
    (content->>'instruction')::TEXT,
    created_at,
    completed_at
FROM wm_node
WHERE kind = 'move';

-- =====================================================================
-- 第 5 步：迁移其他节点回旧 wm_node
-- =====================================================================
INSERT INTO wm_node_old (
    id, task_id, kind, ref, attrs, confidence, created_at, updated_at
)
SELECT
    id,
    task_id,
    CASE kind
        WHEN 'objective' THEN 'target'
        WHEN 'observation' THEN 'asset'
        WHEN 'discovery' THEN 'finding'
        ELSE 'asset'
    END,
    '{}'::JSONB,
    content,
    CASE confidence
        WHEN 'unverified' THEN 'assumed'
        WHEN 'verified' THEN 'confirmed'
        ELSE 'assumed'
    END,
    created_at,
    updated_at
FROM wm_node
WHERE kind != 'move';

-- =====================================================================
-- 第 6 步：迁移边表
-- =====================================================================
INSERT INTO wm_edge_old (id, task_id, rel, src, dst, attrs, created_at)
SELECT
    task_id || '_' || src_id || '_' || rel || '_' || dst_id,
    task_id,
    rel,
    src_id,
    dst_id,
    attrs,
    created_at
FROM wm_edge;

-- =====================================================================
-- 第 7 步：删除新表
-- =====================================================================
DROP TABLE IF EXISTS wm_verification CASCADE;
DROP TABLE IF EXISTS wm_edge CASCADE;
DROP TABLE IF EXISTS wm_node CASCADE;

-- =====================================================================
-- 第 8 步：重命名回原表名
-- =====================================================================
ALTER TABLE wm_node_old RENAME TO wm_node;
ALTER TABLE wm_edge_old RENAME TO wm_edge;
