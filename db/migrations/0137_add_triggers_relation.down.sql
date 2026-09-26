-- Migration 0137 回滚: 移除 TRIGGERS 关系类型

ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_wm_edge_rel;

ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel CHECK (
    rel IN (
        -- Framework 标准关系（恢复到之前的版本，不含 TRIGGERS）
        'GENERATES', 'CONFIRMS', 'REFUTES', 'ENABLES', 'DEPENDS_ON',
        'CONTRIBUTES', 'INVALIDATES',
        -- 旧业务关系（兼容）
        'derives', 'enables', 'on'
    )
);

COMMENT ON CONSTRAINT ck_wm_edge_rel ON wm_edge IS
    'Framework 标准关系: GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON/CONTRIBUTES/INVALIDATES';
