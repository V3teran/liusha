-- 探索图的关系收敛为五种。
--
-- 去掉 serves（动作或线索「推进」某个派生目标）：派生目标只是规划上的分组，没有任何判断
-- 依赖这条边，它却要规划者在每个动作上多声明一个方向，还带着「线索的方向归属只能改一次」
-- 的特例。已有的 serves 边随约束一起删除。
--
-- attaches 更名 covers：它说的是「在办的动作覆盖了这条线索，线索由它的结局结算」。原名与
-- 对话附件（attachment）撞词，中文「挂接」又与「跟进」难以区分。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
DELETE FROM exploration_edge WHERE rel = 'serves';
UPDATE exploration_edge SET rel = 'covers' WHERE rel = 'attaches';
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text, 'pursues'::text, 'covers'::text])));

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / supersedes / pursues / covers（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生目标或动作) / supersedes(新派生目标吸收旧目标，src=新、dst=旧) / pursues(动作为跟进线索而排出，src=动作、dst=线索) / covers(在办动作覆盖线索，只记账，src=动作、dst=线索)';
