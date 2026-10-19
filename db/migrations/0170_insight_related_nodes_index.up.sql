-- 0170: 为 insight.related_nodes 添加 GIN 索引
--
-- 理由：
--   related_nodes 是 JSONB 数组，用于追溯情报来源
--   查询场景：根据节点 ID 查找相关情报（WHERE related_nodes @> '["node_id"]'）
--   GIN 索引专为 JSONB 包含查询优化

-- 注意：不能用 CONCURRENTLY——golang-migrate 的 postgres 驱动与 dbtest 都在事务块内
-- 应用迁移，CONCURRENTLY 无法在其中执行。related_nodes 为新列、表数据量小，普通索引即可。
CREATE INDEX IF NOT EXISTS idx_insight_related_nodes
ON insight USING GIN (related_nodes);

COMMENT ON INDEX idx_insight_related_nodes IS '优化根据探索图节点查询情报';
