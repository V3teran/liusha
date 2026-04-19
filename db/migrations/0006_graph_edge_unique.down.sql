-- 0006 down: 移除 graph_edge 的语义唯一约束。
ALTER TABLE graph_edge DROP CONSTRAINT IF EXISTS graph_edge_unique_rel;
