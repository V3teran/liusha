-- 回滚：先清掉 attaches 与 serves 边，再还原约束与注释。
DELETE FROM exploration_edge WHERE rel IN ('attaches', 'serves');

ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text, 'pursues'::text])));

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / supersedes（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生目标或动作) / supersedes(新派生目标吸收旧目标，src=新、dst=旧)';
