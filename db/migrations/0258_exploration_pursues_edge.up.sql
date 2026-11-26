-- 待深挖线索的跟进关系：exploration_edge.rel 增加 'pursues'——规划层为跟进某条待深挖
-- 线索排出的动作，落一条 action → observation 的 pursues 边。跟进关系因此在图上可查、
-- 可回溯；一条线索是否仍待深挖由图结构推导（标了 opens_deeper、且当前无在办动作经
-- pursues 跟进它），动作被撤回或否决后线索自动回到待深挖。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge
    ADD CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text, 'supersedes'::text, 'pursues'::text])));
