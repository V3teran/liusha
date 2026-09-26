-- evaluation 节点废弃：节点模型收敛为 4 种（objective / action / observation / result）。
-- write_evidence 工具改为产 observation（content.type=evidence），
-- 验证结论由 Evaluator 复现晋升门直接落 result。
DELETE FROM wm_node WHERE kind = 'evaluation';  -- wm_edge 双 FK ON DELETE CASCADE 级联清理关联边

ALTER TABLE wm_node DROP CONSTRAINT ck_wm_node_kind;
ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_kind
    CHECK (kind = ANY (ARRAY['objective'::text, 'action'::text, 'observation'::text, 'result'::text]));
