-- 0061: http_flow 删 agent_id 列（Phase 3.11 简化）
--
-- 背景：
--   0060 加 agent_id 字段意图标识"哪个 agent 发的流量"，但实测：
--   - 容器架构 planner 起容器，exploitation 复用同容器（active_spawner.go:243）
--   - HTTP_PROXY 是容器级 env，所有 agent 共享 planner.task_id
--   - http_flow.agent_id 永远是 planner 的 task_id，不能区分 exploitation
--   - LLM 工具（list_flows/view_flow/replay_flow）按 owner_id 过滤，从不查 agent_id
--   - ingestor 写入时 agent.GetByID 反查 owner 是无谓的额外 DB IO
--
-- 简化：HTTP_PROXY 用 owner_<id> 替代 agent_<id>，sanitizer 直接解析出 owner_id
-- 写入；agent_id 列删除（不留兼容字段）。
--
-- 字段变更：
--   - agent_id        uuid NULL FK→agent(id)
-- 索引变更：
--   - http_flow_agent_idx (agent_id) WHERE agent_id IS NOT NULL

DROP INDEX IF EXISTS http_flow_agent_idx;
ALTER TABLE http_flow DROP COLUMN agent_id;
