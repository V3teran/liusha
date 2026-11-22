-- 发现台账的列、约束与索引按实际语义收紧。
--
-- 1. 一个坐实结果一条发现：结果节点全局唯一（UUID），幂等键本就是结果节点本身，
--    (task_id, result_node_id) 里的 task_id 不增加区分度。唯一约束改为结果节点单列，它同时是
--    结果节点外键的查找索引。列仍可空：0230 之前无法回填的历史记录保留、不关联（空值互不冲突）。
-- 2. first_seen_at 恒等于 created_at：交付按结果节点幂等，重放不改写记录，「首次」与「本次」
--    不会分叉。删除该列。
-- 3. 严重度与发现类型是封闭词表（internal/finding：Severities、class.go），库里补上约束，与状态
--    同一口径。严重度先规整存量：大小写不一的收为小写，词表外的归 medium（交付的缺省值）。
-- 4. 索引：(task_id, created_at DESC) 有两个同定义的副本；target 上的 GIN 与 target->>'host'
--    表达式索引没有查询使用（主机是独立的 host 列，target 里没有 host 键）。

DROP INDEX finding_task_result_uniq;
ALTER TABLE finding ADD CONSTRAINT finding_result_node_uniq UNIQUE (result_node_id);

ALTER TABLE finding DROP COLUMN first_seen_at;

UPDATE finding SET severity = lower(severity) WHERE severity <> lower(severity);
UPDATE finding SET severity = 'medium'
WHERE severity NOT IN ('critical', 'high', 'medium', 'low', 'info');
ALTER TABLE finding ADD CONSTRAINT finding_severity_check
    CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info'));

ALTER TABLE finding ADD CONSTRAINT finding_class_check
    CHECK (class IN (
        'reflected_xss', 'stored_xss', 'dom_xss', 'sql_injection', 'command_injection', 'ssti',
        'path_traversal', 'arbitrary_file_upload', 'ssrf', 'auth_bypass', 'access_control',
        'info_disclosure', 'security_misconfiguration', 'weak_credential', 'deserialization',
        'xxe', 'csrf', 'flag', 'other'
    ));

DROP INDEX IF EXISTS idx_finding_task_created;
DROP INDEX IF EXISTS idx_finding_target_gin;
DROP INDEX IF EXISTS idx_finding_target_host;
