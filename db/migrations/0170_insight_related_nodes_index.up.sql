-- 0170: 为 insight.related_nodes 添加 GIN 索引
--
-- 理由：
--   related_nodes 是 JSONB 数组，用于追溯情报来源
--   查询场景：根据节点 ID 查找相关情报（WHERE related_nodes @> '["node_id"]'）
--   GIN 索引专为 JSONB 包含查询优化

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_insight_related_nodes
ON insight USING GIN (related_nodes);

COMMENT ON INDEX idx_insight_related_nodes IS '优化根据探索图节点查询情报';
