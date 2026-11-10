-- 派生目标的修订收敛（吸收语义）：
-- 1. exploration_node.state 增加 'superseded'——新派生目标吸收旧派生目标的范围时，
--    旧目标转 superseded（范围转交，不是放弃），不再作为独立方向进入规划视野。
-- 2. exploration_edge.rel 增加 'supersedes'——吸收关系（新目标 → 旧目标）显式落边，
--    与旧目标的 superseded 状态同一事务写入，沿图可回溯「这个目标被谁吸收」。
ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_state;
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_state CHECK (((state IS NULL) OR (state = ANY (ARRAY['open'::text, 'running'::text, 'completed'::text, 'failed'::text, 'exhausted'::text, 'aborted'::text, 'superseded'::text]))));

ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text])));
