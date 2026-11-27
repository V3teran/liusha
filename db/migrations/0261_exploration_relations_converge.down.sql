-- 回滚：covers 改回 attaches，约束恢复 serves 的取值（已删除的 serves 边无法恢复）。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
UPDATE exploration_edge SET rel = 'attaches' WHERE rel = 'covers';
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text, 'pursues'::text, 'attaches'::text, 'serves'::text])));

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / supersedes / pursues / attaches / serves（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生目标或动作) / supersedes(新派生目标吸收旧目标，src=新、dst=旧) / pursues(动作为跟进线索而排出，src=动作、dst=线索) / attaches(在办动作挂到线索上，只记账，src=动作、dst=线索) / serves(推进某个派生目标，src=动作或线索、dst=方向)';
