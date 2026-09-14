-- Migration 0135: 添加 Framework GraphStore 所需的数据库结构
--
-- 目的：支持 Framework（ADK）的完整功能
-- 1. wm_node.metadata: 灵活的元数据存储
-- 2. wm_node.roadmap_step: Action 与 RoadmapStep 的关联
-- 3. wm_roadmap_step 表: 独立的 Roadmap 管理
-- 4. 更新约束以支持 Framework 标准节点类型和关系

-- 1. 添加 wm_node.metadata 列（JSONB 类型）
ALTER TABLE wm_node ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}'::jsonb;

-- 2. 添加 wm_node.roadmap_step 列
ALTER TABLE wm_node ADD COLUMN IF NOT EXISTS roadmap_step REAL;

-- 3. 创建 wm_roadmap_step 表
CREATE TABLE IF NOT EXISTS wm_roadmap_step (
	id SERIAL PRIMARY KEY,
	task_id TEXT NOT NULL,
	step REAL NOT NULL,
	objective TEXT,
	status TEXT DEFAULT 'pending',
	depends_on REAL[],
	context JSONB DEFAULT '{}',
	rationale TEXT,
	created_at TIMESTAMP NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
	UNIQUE(task_id, step)
);

-- 4. 更新 wm_node kind 约束，支持 Framework 标准类型
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_kind;
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_kind;

ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_kind CHECK (
	kind IN (
		'objective', 'action', 'observation', 'evaluation', 'result',
		'hypothesis', 'evidence', 'finding',
		'target', 'asset', 'credential', 'access'
	)
);

-- 5. 更新 wm_node confidence 约束
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_confidence_fields;

ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_confidence_fields CHECK (
	(kind IN ('hypothesis', 'finding', 'observation', 'evaluation', 'result') AND confidence IS NOT NULL)
	OR kind NOT IN ('hypothesis', 'finding', 'observation', 'evaluation', 'result')
);

-- 6. 更新 wm_node state 约束
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_state_by_kind;

ALTER TABLE wm_node ADD CONSTRAINT ck_state_by_kind CHECK (
	(kind IN ('action', 'objective') AND state IS NOT NULL)
	OR kind NOT IN ('action', 'objective')
);

-- 7. 更新 wm_edge rel 约束，支持 Framework 标准关系
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_rel;
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_wm_edge_rel;

ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel CHECK (
	rel IN (
		'GENERATES', 'CONFIRMS', 'REFUTES', 'ENABLES', 'DEPENDS_ON',
		'CONTRIBUTES', 'INVALIDATES',
		'derives', 'enables', 'on'
	)
);

-- 8. 创建索引
CREATE INDEX IF NOT EXISTS idx_wm_node_metadata_gin ON wm_node USING gin(metadata);
CREATE INDEX IF NOT EXISTS idx_wm_node_roadmap_step ON wm_node(task_id, roadmap_step);
CREATE INDEX IF NOT EXISTS idx_roadmap_task_step ON wm_roadmap_step(task_id, step);

-- 注释
COMMENT ON COLUMN wm_node.metadata IS 'GraphStore 元数据字段，存储业务特定属性';
COMMENT ON COLUMN wm_node.roadmap_step IS '关联的 RoadmapStep 编号（如果此 Action 由 Roadmap 派发）';
COMMENT ON TABLE wm_roadmap_step IS 'Roadmap 步骤管理表，用于高层规划';
COMMENT ON CONSTRAINT ck_wm_node_kind ON wm_node IS 'Framework 标准类型: objective/action/observation/evaluation/result';
COMMENT ON CONSTRAINT ck_wm_edge_rel ON wm_edge IS 'Framework 标准关系: GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON/CONTRIBUTES/INVALIDATES';
