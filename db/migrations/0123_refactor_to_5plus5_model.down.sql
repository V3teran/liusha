-- Migration 0123: 回滚到 0121 状态（4 种节点 + 6 种关系）
-- 本回滚脚本恢复到迁移前的表结构

-- =====================================================================
-- 第 1 步：删除 5+5 模型的表
-- =====================================================================
DROP TRIGGER IF EXISTS trg_wm_node_updated_at ON wm_node;
DROP FUNCTION IF EXISTS update_wm_node_updated_at();

DROP TABLE IF EXISTS wm_verification CASCADE;
DROP TABLE IF EXISTS wm_edge CASCADE;
DROP TABLE IF EXISTS wm_node CASCADE;

-- =====================================================================
-- 第 2 步：恢复 0121 的 wm_node 表（4 种节点）
-- =====================================================================
CREATE TABLE wm_node (
    id             TEXT PRIMARY KEY,
    task_id        TEXT NOT NULL,
    kind           TEXT NOT NULL,
    content        JSONB NOT NULL DEFAULT '{}',
    state          TEXT,
    complexity     TEXT,
    depends_on     TEXT[] DEFAULT '{}',
    blocked_reason TEXT,
    confidence     TEXT,
    priority       INT NOT NULL DEFAULT 50,
    owner          TEXT,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    tags           TEXT[] DEFAULT '{}',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,

    CONSTRAINT ck_kind CHECK (kind IN ('objective', 'move', 'observation', 'discovery')),
    CONSTRAINT ck_state CHECK (
        state IS NULL OR
        state IN ('open', 'blocked', 'running', 'done', 'failed', 'exhausted', 'aborted')
    ),
    CONSTRAINT ck_complexity CHECK (
        complexity IS NULL OR
        complexity IN ('trivial', 'simple', 'moderate', 'complex', 'extreme')
    ),
    CONSTRAINT ck_confidence CHECK (
        confidence IS NULL OR
        confidence IN ('unverified', 'verified')
    )
);

CREATE INDEX idx_wm_node_task_id ON wm_node(task_id);
CREATE INDEX idx_wm_node_task_kind ON wm_node(task_id, kind);
CREATE INDEX idx_wm_node_task_state ON wm_node(task_id, state) WHERE state IS NOT NULL;

-- =====================================================================
-- 第 3 步：恢复 0121 的 wm_edge 表（6 种关系）
-- =====================================================================
CREATE TABLE wm_edge (
    task_id    TEXT NOT NULL,
    src_id     TEXT NOT NULL,
    rel        TEXT NOT NULL,
    dst_id     TEXT NOT NULL,
    attrs      JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (task_id, src_id, rel, dst_id),

    CONSTRAINT fk_wm_edge_src FOREIGN KEY (src_id) REFERENCES wm_node(id) ON DELETE CASCADE,
    CONSTRAINT fk_wm_edge_dst FOREIGN KEY (dst_id) REFERENCES wm_node(id) ON DELETE CASCADE,
    CONSTRAINT ck_rel CHECK (rel IN ('produces', 'supports', 'refutes', 'enables', 'derives', 'part_of'))
);

CREATE INDEX idx_wm_edge_dst ON wm_edge(task_id, dst_id);

-- =====================================================================
-- 第 4 步：恢复 wm_verification 表
-- =====================================================================
CREATE TABLE wm_verification (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    primitives  JSONB NOT NULL DEFAULT '{}',
    outcome     TEXT NOT NULL,
    evidence    JSONB NOT NULL DEFAULT '{}',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT fk_wm_verification_node FOREIGN KEY (node_id) REFERENCES wm_node(id) ON DELETE CASCADE,
    CONSTRAINT ck_outcome CHECK (outcome IN ('confirmed', 'refuted'))
);

CREATE INDEX idx_wm_verification_task_id ON wm_verification(task_id);
CREATE INDEX idx_wm_verification_node_id ON wm_verification(node_id);

-- =====================================================================
-- 第 5 步：恢复触发器
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
