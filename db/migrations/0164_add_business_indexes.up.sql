-- 添加业务索引，优化高频查询性能
--
-- 注意：不使用 CONCURRENTLY，因为 migrate 工具在事务中执行。
-- 分批执行，每个索引独立，失败不影响其他索引。

-- 1. finding 表：severity + status 复合索引（支持过滤查询）
--    查询场景：SELECT * FROM finding WHERE severity='critical' AND status='open';
CREATE INDEX IF NOT EXISTS idx_finding_severity_status
ON finding(severity, status)
WHERE status IN ('open', 'confirmed');

-- 2. finding 表：task + 创建时间（支持分页查询）
--    查询场景：SELECT * FROM finding WHERE task_id='xxx' ORDER BY created_at DESC LIMIT 20;
CREATE INDEX IF NOT EXISTS idx_finding_task_created
ON finding(task_id, created_at DESC);

-- 3. exploration_node 表：task + kind + state 复合索引（支持状态过滤）
--    查询场景：SELECT * FROM exploration_node WHERE task_id='xxx' AND kind='action' AND state='running';
CREATE INDEX IF NOT EXISTS idx_expl_node_task_kind_state
ON exploration_node(task_id, kind, state)
WHERE state IN ('running', 'open', 'blocked');

-- 4. finding.target GIN 索引（支持 jsonb 查询）
--    查询场景：SELECT * FROM finding WHERE target @> '{"host":"example.com"}';
CREATE INDEX IF NOT EXISTS idx_finding_target_gin
ON finding USING GIN (target);

-- 5. finding.target.host 表达式索引（高频查询优化）
--    查询场景：SELECT * FROM finding WHERE target->>'host' = 'example.com';
CREATE INDEX IF NOT EXISTS idx_finding_target_host
ON finding ((target->>'host'));

-- 6. exploration_node.content GIN 索引（支持 jsonb 查询）
--    查询场景：SELECT * FROM exploration_node WHERE content @> '{"instruction":"..."}';
CREATE INDEX IF NOT EXISTS idx_expl_node_content_gin
ON exploration_node USING GIN (content);

-- 注意事项：
-- 1. 不使用 CONCURRENTLY（migrate 工具在事务中执行）
-- 2. GIN 索引会增加 10-20% 写入开销，只对高频查询的 jsonb 创建
-- 3. 部分索引（WHERE 子句）减少索引大小和维护成本
-- 4. 索引创建期间会短暂阻塞写入（通常 < 1 秒）
