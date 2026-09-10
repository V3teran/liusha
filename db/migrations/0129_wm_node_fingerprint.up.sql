-- 添加 fingerprint 字段用于 Actions 去重
--
-- 目的：避免同时存在多个相同的未完成 Actions
--
-- 策略：只去重未完成的（state IN ('open', 'running')）
--        允许重新扫描、重试已完成的 Actions
--
-- 使用场景：
-- - Planner 创建 Action 前检查是否有相同的未完成 Action
-- - 防止重复规划导致浪费资源
-- - 支持故意的重复扫描（定期监控）

ALTER TABLE wm_node ADD COLUMN IF NOT EXISTS fingerprint TEXT;

-- 添加注释
COMMENT ON COLUMN wm_node.fingerprint IS 'Action 内容指纹（SHA256），用于去重';

-- 创建索引（重要：用于快速查询）
CREATE INDEX IF NOT EXISTS idx_wm_node_fingerprint
ON wm_node(task_id, fingerprint, state)
WHERE kind = 'action';

-- 部分索引：只索引有 fingerprint 的 action 节点
CREATE INDEX IF NOT EXISTS idx_wm_node_fingerprint_pending
ON wm_node(task_id, fingerprint)
WHERE kind = 'action' AND state IN ('open', 'running');
