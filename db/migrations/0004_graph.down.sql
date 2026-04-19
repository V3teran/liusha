-- 0004 down: 先 drop 边表再 drop 节点表，避免 FK 依赖问题。
DROP TABLE IF EXISTS graph_edge;
DROP TABLE IF EXISTS graph_node;
