-- 0170 down: 删除 insight.related_nodes GIN 索引

DROP INDEX CONCURRENTLY IF EXISTS idx_insight_related_nodes;
