-- 添加缺失的外键索引，优化 JOIN 和 CASCADE DELETE 性能
--
-- 背景：外键列应该有索引（业界最佳实践），支持：
--   1. JOIN 查询性能（避免全表扫描）
--   2. CASCADE DELETE 性能（快速定位子记录）
--
-- 发现的缺失索引：
--   - agent_traffic.agent_run_id
--   - finding.agent_run_id
--   - tool_invocation.agent_run_id
--   - llm_role_route.provider_code
--
-- 注意：不使用 CONCURRENTLY，因为 migrate 工具在事务中执行。

-- 1. agent_traffic.agent_run_id 索引（支持按 agent_run 查询流量）
CREATE INDEX IF NOT EXISTS idx_agent_traffic_agent_run_id
ON agent_traffic(agent_run_id);

-- 2. finding.agent_run_id 索引（支持按 agent_run 查询发现）
CREATE INDEX IF NOT EXISTS idx_finding_agent_run_id
ON finding(agent_run_id);

-- 3. tool_invocation.agent_run_id 索引（支持按 agent_run 查询工具调用）
CREATE INDEX IF NOT EXISTS idx_tool_invocation_agent_run_id
ON tool_invocation(agent_run_id);

-- 4. llm_role_route.provider_code 索引（支持按 provider 查询路由）
CREATE INDEX IF NOT EXISTS idx_llm_role_route_provider_code
ON llm_role_route(provider_code);

-- 预期收益：
--   - JOIN 查询性能提升 10-50 倍
--   - CASCADE DELETE 性能提升 5-10 倍
