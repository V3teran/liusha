-- 0170 down: 删除 insight.related_nodes GIN 索引

-- 与 up 对应：不用 CONCURRENTLY（事务块内不可执行）
DROP INDEX IF EXISTS idx_insight_related_nodes;
