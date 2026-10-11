-- 回滚外键索引

DROP INDEX IF EXISTS idx_agent_traffic_agent_run_id;
DROP INDEX IF EXISTS idx_finding_agent_run_id;
DROP INDEX IF EXISTS idx_tool_invocation_agent_run_id;
DROP INDEX IF EXISTS idx_llm_role_route_provider_code;
