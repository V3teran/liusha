-- 回滚 0113：agent → executor

-- 表改名
ALTER TABLE agent RENAME TO executor;
ALTER TABLE agent_run RENAME TO executor_run;

-- kind 枚举恢复
ALTER TABLE executor DROP CONSTRAINT agent_kind_check;
UPDATE executor SET kind = 'planner' WHERE kind = 'planner';
UPDATE executor SET kind = 'domain' WHERE kind = 'executor';
ALTER TABLE executor ADD CONSTRAINT executor_kind_check
    CHECK (kind IN ('planner', 'domain'));

-- 列改名
ALTER TABLE executor_run RENAME COLUMN agent_id TO executor_id;
ALTER TABLE finding RENAME COLUMN agent_run_id TO executor_run_id;
ALTER TABLE llm_invocation RENAME COLUMN agent_run_id TO executor_run_id;
ALTER TABLE tool_invocation RENAME COLUMN agent_run_id TO executor_run_id;
ALTER TABLE agent_traffic RENAME COLUMN agent_run_id TO executor_run_id;
ALTER TABLE scenario RENAME COLUMN solo_agent_id TO solo_executor_id;

-- 约束改名
ALTER TABLE executor RENAME CONSTRAINT agent_pkey TO executor_pkey;
ALTER TABLE executor RENAME CONSTRAINT agent_owner_type_check TO executor_owner_type_check;
ALTER TABLE executor RENAME CONSTRAINT agent_status_check TO executor_status_check;
ALTER TABLE executor RENAME CONSTRAINT agent_role_check TO executor_role_check;

ALTER TABLE executor_run RENAME CONSTRAINT agent_run_pkey TO executor_run_pkey;
ALTER TABLE executor_run RENAME CONSTRAINT agent_run_agent_id_fkey TO executor_run_executor_id_fkey;

ALTER TABLE finding RENAME CONSTRAINT finding_agent_run_id_fkey TO finding_executor_run_id_fkey;
ALTER TABLE llm_invocation RENAME CONSTRAINT llm_invocation_agent_run_id_fkey TO llm_invocation_executor_run_id_fkey;
ALTER TABLE tool_invocation RENAME CONSTRAINT tool_invocation_agent_run_id_fkey TO tool_invocation_executor_run_id_fkey;
ALTER TABLE agent_traffic RENAME CONSTRAINT agent_traffic_agent_run_id_fkey TO agent_traffic_executor_run_id_fkey;
ALTER TABLE scenario RENAME CONSTRAINT scenario_solo_agent_id_fkey TO scenario_solo_executor_id_fkey;

-- 索引改名
ALTER INDEX agent_owner_idx RENAME TO executor_owner_idx;
ALTER INDEX agent_run_agent_id_idx RENAME TO executor_run_executor_id_idx;
