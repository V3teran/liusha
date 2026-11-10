-- 回滚目标收敛：先清数据（收敛边与收敛态），再还原约束。
DELETE FROM exploration_edge WHERE rel = 'supersedes';
UPDATE exploration_node SET state = 'aborted' WHERE state = 'superseded';

ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text])));

ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_state;
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_state CHECK (((state IS NULL) OR (state = ANY (ARRAY['open'::text, 'running'::text, 'completed'::text, 'failed'::text, 'exhausted'::text, 'aborted'::text]))));
