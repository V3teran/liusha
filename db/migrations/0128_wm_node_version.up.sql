-- 添加 version 字段用于乐观锁（CAS）
--
-- 目的：防止多个 Orchestrator 实例同时修改同一个 Action
--
-- 原理：UPDATE 时检查 version，只有匹配时才更新
--
-- 使用场景：
-- - 多实例部署时，防止重复执行
-- - Orchestrator 执行前 CAS：open → running
-- - Monitor kill 时 CAS：running → aborted

ALTER TABLE wm_node ADD COLUMN IF NOT EXISTS version INTEGER DEFAULT 1;

-- 添加注释
COMMENT ON COLUMN wm_node.version IS '乐观锁版本号，每次更新自动递增';

-- 创建索引（可选，用于调试和监控）
CREATE INDEX IF NOT EXISTS idx_wm_node_version ON wm_node(task_id, version);
