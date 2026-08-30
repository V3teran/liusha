-- Migration 0123: 重构为 5+5 世界模型
-- 设计决策：
-- 1. 节点类型（5种）：objective/action/hypothesis/evidence/finding
-- 2. 关系类型（5种）：GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON
-- 3. 对标科学方法论：目标 → 动作 → 假设 → 证据 → 发现
-- 4. 清空数据库，全新开始

-- =====================================================================
-- 第 1 步：删除旧表（清空数据库）
-- =====================================================================
DROP TABLE IF EXISTS wm_verification CASCADE;
DROP TABLE IF EXISTS wm_edge CASCADE;
DROP TABLE IF EXISTS wm_node CASCADE;
DROP TABLE IF EXISTS wm_node_legacy CASCADE;
DROP TABLE IF EXISTS wm_edge_legacy CASCADE;
DROP TABLE IF EXISTS wm_verification_legacy CASCADE;

-- =====================================================================
-- 第 2 步：创建新的 wm_node 表（5 种节点）
-- =====================================================================
CREATE TABLE wm_node (
    id             TEXT PRIMARY KEY,
    task_id        TEXT NOT NULL,
    kind           TEXT NOT NULL,
    content        JSONB NOT NULL DEFAULT '{}',

    -- action 专用字段（kind='action' 时使用）
    state          TEXT,
    complexity     TEXT,
    depends_on     TEXT[] DEFAULT '{}',
    blocked_reason TEXT,

    -- hypothesis/finding 专用字段（kind='hypothesis'/'finding' 时使用）
    confidence     TEXT,

    -- 通用字段
    priority       INT NOT NULL DEFAULT 50,
    owner          TEXT,
    source_type    TEXT NOT NULL,
    source_id      TEXT NOT NULL,
    tags           TEXT[] DEFAULT '{}',
    metadata       JSONB DEFAULT '{}',

    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,

    -- 约束：节点类型必须是 5 种之一
    CONSTRAINT ck_wm_node_kind CHECK (kind IN ('objective', 'action', 'hypothesis', 'evidence', 'finding')),

    -- 约束：action 必须有 state 和 complexity
    CONSTRAINT ck_wm_node_action_fields CHECK (
        (kind = 'action' AND state IS NOT NULL AND complexity IS NOT NULL)
        OR kind != 'action'
    ),

    -- 约束：state 枚举值
    CONSTRAINT ck_wm_node_state CHECK (
        state IS NULL OR
        state IN ('open', 'blocked', 'running', 'done', 'failed', 'exhausted', 'aborted')
    ),

    -- 约束：complexity 枚举值
    CONSTRAINT ck_wm_node_complexity CHECK (
        complexity IS NULL OR
        complexity IN ('trivial', 'simple', 'moderate', 'complex', 'extreme')
    ),

    -- 约束：hypothesis/finding 必须有 confidence
    CONSTRAINT ck_wm_node_confidence_fields CHECK (
        (kind IN ('hypothesis', 'finding') AND confidence IS NOT NULL)
        OR kind NOT IN ('hypothesis', 'finding')
    ),

    -- 约束：confidence 枚举值
    CONSTRAINT ck_wm_node_confidence CHECK (
        confidence IS NULL OR
        confidence IN ('unverified', 'verified')
    ),

    -- 约束：source_type 枚举值
    CONSTRAINT ck_wm_node_source_type CHECK (
        source_type IN ('user', 'planner', 'executor', 'verifier', 'system')
    )
);

-- 索引
CREATE INDEX idx_wm_node_task_id ON wm_node(task_id);
CREATE INDEX idx_wm_node_task_kind ON wm_node(task_id, kind);
CREATE INDEX idx_wm_node_task_state ON wm_node(task_id, state) WHERE state IS NOT NULL;
CREATE INDEX idx_wm_node_task_confidence ON wm_node(task_id, confidence) WHERE confidence IS NOT NULL;
CREATE INDEX idx_wm_node_tags ON wm_node USING GIN(tags);
CREATE INDEX idx_wm_node_metadata ON wm_node USING GIN(metadata);

-- =====================================================================
-- 第 3 步：创建新的 wm_edge 表（5 种关系）
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

    -- 约束：关系类型必须是 5 种之一（全大写）
    CONSTRAINT ck_wm_edge_rel CHECK (rel IN ('GENERATES', 'CONFIRMS', 'REFUTES', 'ENABLES', 'DEPENDS_ON'))
);

-- 索引（支持反向查询）
CREATE INDEX idx_wm_edge_dst ON wm_edge(task_id, dst_id);
CREATE INDEX idx_wm_edge_rel ON wm_edge(task_id, rel);

-- =====================================================================
-- 第 4 步：创建新的 wm_verification 表
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

    -- 外键约束
    CONSTRAINT fk_wm_verification_node FOREIGN KEY (node_id) REFERENCES wm_node(id) ON DELETE CASCADE,

    -- 约束：outcome 枚举值
    CONSTRAINT ck_wm_verification_outcome CHECK (outcome IN ('confirmed', 'refuted'))
);

-- 索引
CREATE INDEX idx_wm_verification_task_id ON wm_verification(task_id);
CREATE INDEX idx_wm_verification_node_id ON wm_verification(node_id);
CREATE INDEX idx_wm_verification_outcome ON wm_verification(task_id, outcome);

-- =====================================================================
-- 第 5 步：触发器（自动更新 updated_at）
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

-- =====================================================================
-- 第 6 步：注释（文档化）
-- =====================================================================
COMMENT ON TABLE wm_node IS '世界模型节点表（5种类型）：objective/action/hypothesis/evidence/finding';
COMMENT ON COLUMN wm_node.kind IS '节点类型：objective(目标)/action(动作)/hypothesis(假设)/evidence(证据)/finding(发现)';
COMMENT ON COLUMN wm_node.state IS 'action 专用：执行状态 open/blocked/running/done/failed/exhausted/aborted';
COMMENT ON COLUMN wm_node.complexity IS 'action 专用：复杂度 trivial/simple/moderate/complex/extreme';
COMMENT ON COLUMN wm_node.depends_on IS 'action 专用：依赖的其他 action ID 列表';
COMMENT ON COLUMN wm_node.confidence IS 'hypothesis/finding 专用：置信度 unverified/verified';
COMMENT ON COLUMN wm_node.tags IS '自由标签，支持 GIN 索引查询';
COMMENT ON COLUMN wm_node.metadata IS '扩展元数据，JSONB 格式，支持 GIN 索引查询';

COMMENT ON TABLE wm_edge IS '世界模型关系边表（5种关系）：GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON';
COMMENT ON COLUMN wm_edge.rel IS '关系类型（全大写）：GENERATES(生成)/CONFIRMS(确认)/REFUTES(反驳)/ENABLES(使能)/DEPENDS_ON(依赖)';

COMMENT ON TABLE wm_verification IS '验证记录表（审计链）';
COMMENT ON COLUMN wm_verification.outcome IS '验证结果：confirmed(确认)/refuted(反驳)';
