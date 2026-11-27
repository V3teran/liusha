-- 回滚：约束恢复 covers / supersedes 与 superseded 状态的取值（已删除的 covers / supersedes 边、
-- 已改写成 aborted 的 superseded 状态无法恢复）。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text, 'pursues'::text, 'covers'::text])));

ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_state;
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_state CHECK ((state IS NULL) OR (state = ANY (ARRAY['open'::text, 'running'::text, 'completed'::text, 'failed'::text, 'exhausted'::text, 'aborted'::text, 'rejected'::text, 'superseded'::text])));

COMMENT ON TABLE public.exploration_edge IS '探索图关系边：generates / triggers / supersedes / pursues / covers（见 ck_exploration_edge_rel）';
COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型：generates(推导链) / triggers(结果催生目标或动作) / supersedes(新派生目标吸收旧目标，src=新、dst=旧) / pursues(动作为跟进线索而排出，src=动作、dst=线索) / covers(在办动作覆盖线索，只记账，src=动作、dst=线索)';
COMMENT ON COLUMN public.exploration_node.state IS 'action/objective 的状态：open/running/completed/failed/exhausted/aborted（没做完即被收回，不含对错判断）/rejected（action 专用：被监察者或用户否决，见 metadata.rejected_by）/superseded（派生目标被吸收）';
