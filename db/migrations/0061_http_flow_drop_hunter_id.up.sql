-- 0061: http_flow 删 hunter_id 列（Phase 3.11 简化）
--
-- 背景：
--   0060 加 hunter_id 字段意图标识"哪个 hunter 发的流量"，但实测：
--   - 容器架构 commander 起容器，striker 复用同容器（active_spawner.go:243）
--   - HTTP_PROXY 是容器级 env，所有 hunter 共享 commander.task_id
--   - http_flow.hunter_id 永远是 commander 的 task_id，不能区分 striker
--   - LLM 工具（list_flows/view_flow/replay_flow）按 owner_id 过滤，从不查 hunter_id
--   - ingestor 写入时 hunter.GetByID 反查 owner 是无谓的额外 DB IO
--
-- 简化：HTTP_PROXY 用 owner_<id> 替代 hunter_<id>，sanitizer 直接解析出 owner_id
-- 写入；hunter_id 列删除（不留兼容字段）。
--
-- 字段变更：
--   - hunter_id        uuid NULL FK→hunter(id)
-- 索引变更：
--   - http_flow_hunter_idx (hunter_id) WHERE hunter_id IS NOT NULL

DROP INDEX IF EXISTS http_flow_hunter_idx;
ALTER TABLE http_flow DROP COLUMN hunter_id;
