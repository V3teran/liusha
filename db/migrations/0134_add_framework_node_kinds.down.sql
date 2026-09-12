-- Migration 0134 down: 回滚 Framework 标准节点类型支持

-- 1. 恢复原始 kind 约束
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_kind;

ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_kind CHECK (
    kind IN ('objective', 'action', 'hypothesis', 'evidence', 'finding')
);

-- 2. 恢复原始 confidence 约束
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_confidence_fields;

ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_confidence_fields CHECK (
    (kind IN ('hypothesis', 'finding') AND confidence IS NOT NULL)
    OR kind NOT IN ('hypothesis', 'finding')
);

-- 3. 恢复原始 rel 约束
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_wm_edge_rel;

ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel CHECK (
    rel IN ('GENERATES', 'CONFIRMS', 'REFUTES', 'ENABLES', 'DEPENDS_ON')
);
