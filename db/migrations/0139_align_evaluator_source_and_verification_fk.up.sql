-- 1) wm_node.source_type：verifier → evaluator（与四智能体架构的组件命名对齐）
UPDATE wm_node SET source_type = 'evaluator' WHERE source_type = 'verifier';
ALTER TABLE wm_node DROP CONSTRAINT ck_wm_node_source_type;
ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_source_type
    CHECK (source_type = ANY (ARRAY['user'::text, 'planner'::text, 'executor'::text, 'evaluator'::text, 'system'::text]));

-- 2) wm_verification.node_id 的 FK 与设计矛盾：证伪的验证不产生图节点，
--    但审计链必须落档（node_id 是溯源指针，不保证对应存在的节点）。
ALTER TABLE wm_verification DROP CONSTRAINT fk_wm_verification_node;
