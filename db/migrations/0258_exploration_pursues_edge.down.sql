-- 回滚跟进关系：先清 pursues 边，再还原约束。
DELETE FROM exploration_edge WHERE rel = 'pursues';

ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text])));
