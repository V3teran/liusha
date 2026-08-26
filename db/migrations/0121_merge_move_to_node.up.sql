-- Migration 0121: 合并 planned_move 到 wm_node，统一图模型
-- 设计决策：
-- 1. Move 变成 kind='move' 的 Node
-- 2. 4 种 NodeKind: objective/move/observation/discovery
-- 3. 2 级 Confidence: unverified/verified
-- 4. 独立边表 wm_edge

-- =====================================================================
-- 第 1 步：重命名现有 wm_node 表（临时备份）
-- =====================================================================
ALTER TABLE wm_node RENAME TO wm_node_legacy;
ALTER TABLE wm_edge RENAME TO wm_edge_legacy;
ALTER TABLE wm_verification RENAME TO wm_verification_legacy;

-- =====================================================================
-- 第 2 步：创建新的 wm_node 表（统一模型）
-- =====================================================================
CREATE TABLE wm_node (
    id             TEXT PRIMARY KEY,
    task_id        TEXT NOT NULL,
    kind           TEXT NOT NULL,
    content        JSONB NOT NULL DEFAULT '{}',

    -- move 专用字段
    state          TEXT,
    complexity     TEXT,
    depends_on     TEXT[] DEFAULT '{}',
    blocked_reason TEXT,

    -- observation/discovery 专用字段
    confidence     TEXT,

    -- 通用字段
    priority       INT NOT NULL DEFAULT 0,
    owner          TEXT,
    source_type    TEXT,
    source_id      TEXT,
    tags           TEXT[] DEFAULT '{}',

    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,

    -- 约束：kind 枚举
    CONSTRAINT ck_kind CHECK (kind IN ('objective', 'move', 'observation', 'discovery')),

    -- 约束：state/confidence 互斥
    CONSTRAINT ck_state_by_kind CHECK (
        (kind = 'objective' AND state IS NULL AND confidence IS NULL AND complexity IS NULL) OR
        (kind = 'move' AND state IS NOT NULL AND confidence IS NULL AND complexity IS NOT NULL) OR
        (kind = 'observation' AND state IS NULL AND confidence IS NOT NULL AND complexity IS NULL) OR
        (kind = 'discovery' AND state IS NULL AND confidence IS NOT NULL AND complexity IS NULL)
    ),

    -- 约束：move 的 state 值域
    CONSTRAINT ck_move_state CHECK (
        kind != 'move' OR state IN ('open', 'blocked', 'running', 'done', 'failed', 'exhausted', 'aborted')
    ),

    -- 约束：confidence 值域
    CONSTRAINT ck_confidence CHECK (
        confidence IS NULL OR confidence IN ('unverified', 'verified')
    ),

    -- 约束：complexity 值域
    CONSTRAINT ck_complexity CHECK (
        complexity IS NULL OR complexity IN ('trivial', 'simple', 'moderate', 'complex', 'extreme')
    )
);

-- 索引
CREATE INDEX idx_wm_node_task_kind ON wm_node(task_id, kind);
CREATE INDEX idx_wm_node_task_state ON wm_node(task_id, state) WHERE kind = 'move';
CREATE INDEX idx_wm_node_priority ON wm_node(priority DESC);
CREATE INDEX idx_wm_node_owner ON wm_node(owner) WHERE owner IS NOT NULL;
CREATE INDEX idx_wm_node_confidence ON wm_node(task_id, confidence) WHERE kind IN ('observation', 'discovery');

-- =====================================================================
-- 第 3 步：创建新的 wm_edge 表（独立边表）
-- =====================================================================
CREATE TABLE wm_edge (
    task_id    TEXT NOT NULL,
    src_id     TEXT NOT NULL,
    rel        TEXT NOT NULL,
    dst_id     TEXT NOT NULL,
    attrs      JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (task_id, src_id, rel, dst_id),

    -- 外键约束
    CONSTRAINT fk_wm_edge_src FOREIGN KEY (src_id) REFERENCES wm_node(id) ON DELETE CASCADE,
    CONSTRAINT fk_wm_edge_dst FOREIGN KEY (dst_id) REFERENCES wm_node(id) ON DELETE CASCADE,

    -- 关系类型约束
    CONSTRAINT ck_rel CHECK (rel IN ('produces', 'supports', 'refutes', 'enables', 'derives', 'part_of'))
);

-- 索引（支持反向查询）
CREATE INDEX idx_wm_edge_dst ON wm_edge(task_id, dst_id);
CREATE INDEX idx_wm_edge_rel ON wm_edge(task_id, rel);

-- =====================================================================
-- 第 4 步：迁移 planned_move 数据到 wm_node (kind=move)
-- =====================================================================
INSERT INTO wm_node (
    id, task_id, kind, content, state, complexity, depends_on, priority,
    source_type, created_at, completed_at
)
SELECT
    id::TEXT,
    task_id,
    'move',
    jsonb_build_object(
        'instruction', reason,
        'target_ref', jsonb_build_object(
            'domain', domain,
            'ref_kind', (target_ref->>'ref_kind'),
            'locator', (target_ref->>'locator')
        )
    ),
    -- 状态映射
    CASE status
        WHEN 'pending' THEN 'open'
        WHEN 'executing' THEN 'running'
        WHEN 'completed' THEN 'done'
        WHEN 'failed' THEN 'failed'
        ELSE 'open'
    END,
    -- 根据 kind 映射 complexity
    CASE kind
        WHEN 'enumerate' THEN 'simple'
        WHEN 'probe' THEN 'moderate'
        WHEN 'exploit' THEN 'complex'
        WHEN 'escalate' THEN 'complex'
        WHEN 'persist' THEN 'moderate'
        ELSE 'moderate'
    END,
    depends_on,
    priority,
    'planner',
    created_at,
    completed_at
FROM planned_move;

-- =====================================================================
-- 第 5 步：迁移旧 wm_node 数据（映射到新 kind）
-- =====================================================================
INSERT INTO wm_node (
    id, task_id, kind, content, confidence, priority,
    source_type, created_at, updated_at
)
SELECT
    id,
    task_id,
    -- kind 映射
    CASE kind
        WHEN 'target' THEN 'objective'
        WHEN 'asset' THEN 'observation'
        WHEN 'credential' THEN 'discovery'
        WHEN 'access' THEN 'discovery'
        WHEN 'finding' THEN 'discovery'
        ELSE 'observation'
    END,
    -- content 保持
    attrs,
    -- confidence 映射
    CASE confidence
        WHEN 'assumed' THEN 'unverified'
        WHEN 'confirmed' THEN 'verified'
        ELSE 'unverified'
    END,
    0,  -- priority 默认
    'executor',
    created_at,
    updated_at
FROM wm_node_legacy;

-- =====================================================================
-- 第 6 步：迁移旧 wm_edge 数据
-- =====================================================================
INSERT INTO wm_edge (task_id, src_id, rel, dst_id, attrs, created_at)
SELECT
    task_id,
    src,
    -- 关系映射
    CASE rel
        WHEN 'spawns' THEN 'produces'
        WHEN 'on' THEN 'part_of'
        ELSE rel
    END,
    dst,
    attrs,
    created_at
FROM wm_edge_legacy;

-- =====================================================================
-- 第 7 步：更新 wm_verification 表（引用新 ID）
-- =====================================================================
CREATE TABLE wm_verification (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    node_id     TEXT NOT NULL REFERENCES wm_node(id) ON DELETE CASCADE,
    primitives  JSONB,
    outcome     TEXT NOT NULL CHECK (outcome IN ('confirmed', 'refuted')),
    evidence    JSONB,
    duration_ms BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 迁移数据
INSERT INTO wm_verification (id, task_id, node_id, primitives, outcome, evidence, duration_ms, created_at)
SELECT id, task_id, lead_id, primitives, outcome, evidence, duration_ms, created_at
FROM wm_verification_legacy
WHERE EXISTS (SELECT 1 FROM wm_node WHERE id = lead_id);

-- =====================================================================
-- 第 8 步：删除 planned_move 表和旧表
-- =====================================================================
DROP TABLE IF EXISTS planned_move;
DROP TABLE IF EXISTS wm_node_legacy CASCADE;
DROP TABLE IF EXISTS wm_edge_legacy CASCADE;
DROP TABLE IF EXISTS wm_verification_legacy CASCADE;

-- =====================================================================
-- 第 9 步：触发器（自动更新 updated_at）
-- =====================================================================
CREATE OR REPLACE FUNCTION update_wm_node_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_wm_node_updated_at
    BEFORE UPDATE ON wm_node
    FOR EACH ROW
    EXECUTE FUNCTION update_wm_node_updated_at();
