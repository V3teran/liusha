-- 探索图增加两种关系：
--   attaches（action → observation）：在办的动作挂到一条晚出现的线索上。只记账——线索的
--   结局由这个动作结算；它不改在途动作的范围，所以与「动作为跟进线索而排出」的 pursues
--   分成两种边，执行者只认后者。
--   serves（action → objective / observation → objective）：动作或线索推进某个派生目标。
--   每个动作只有一个归属：追线索的挂在线索下，不追线索的挂在方向下，都不声明的留在
--   根目标下。
-- 表与列的注释一并补全：0258 加 pursues 时漏了。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text, 'pursues'::text, 'attaches'::text, 'serves'::text])));

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / supersedes / pursues / attaches / serves（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生目标或动作) / supersedes(新派生目标吸收旧目标，src=新、dst=旧) / pursues(动作为跟进线索而排出，src=动作、dst=线索) / attaches(在办动作挂到线索上，只记账，src=动作、dst=线索) / serves(推进某个派生目标，src=动作或线索、dst=方向)';
