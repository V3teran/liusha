-- 回滚业务索引

DROP INDEX IF EXISTS idx_finding_severity_status;
DROP INDEX IF EXISTS idx_finding_task_created;
DROP INDEX IF EXISTS idx_expl_node_task_kind_state;
DROP INDEX IF EXISTS idx_finding_target_gin;
DROP INDEX IF EXISTS idx_finding_target_host;
DROP INDEX IF EXISTS idx_expl_node_content_gin;
