-- 节点 kind 收敛到 5 节点世界模型：objective / action / observation / evaluation / result
-- 旧模型（hypothesis/evidence/finding/target/asset/credential/access）残留行清理。
DELETE FROM wm_node WHERE kind NOT IN ('objective', 'action', 'observation', 'evaluation', 'result');

ALTER TABLE wm_node DROP CONSTRAINT ck_wm_node_kind;
ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_kind
    CHECK (kind = ANY (ARRAY['objective'::text, 'action'::text, 'observation'::text,
                             'evaluation'::text, 'result'::text]));
