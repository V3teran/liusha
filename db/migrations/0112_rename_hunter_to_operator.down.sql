-- 回滚表改名
ALTER TABLE executor RENAME TO agent;
ALTER TABLE executor_run RENAME TO agent_run;

-- 回滚 FK 列改名
ALTER TABLE finding RENAME COLUMN executor_run_id TO agent_id;
ALTER TABLE llm_invocation RENAME COLUMN executor_run_id TO agent_id;
ALTER TABLE tool_invocation RENAME COLUMN executor_run_id TO agent_id;
ALTER TABLE agent_traffic RENAME COLUMN executor_run_id TO agent_id;

-- 回滚 scenario 列改名
ALTER TABLE scenario RENAME COLUMN solo_executor_id TO solo_agent_id;

-- 回滚约束改名
ALTER TABLE agent RENAME CONSTRAINT executor_pkey TO agent_pkey;
ALTER TABLE agent RENAME CONSTRAINT executor_owner_type_check TO agent_owner_type_check;
ALTER TABLE agent RENAME CONSTRAINT executor_status_check TO agent_status_check;
ALTER TABLE agent RENAME CONSTRAINT executor_role_check TO agent_role_check;

ALTER TABLE agent_run RENAME CONSTRAINT executor_run_pkey TO agent_run_pkey;
ALTER TABLE agent_run RENAME CONSTRAINT executor_run_executor_id_fkey TO agent_run_agent_id_fkey;

-- 回滚索引改名
ALTER INDEX executor_owner_idx RENAME TO agent_owner_idx;
ALTER INDEX executor_run_executor_id_idx RENAME TO agent_run_agent_id_idx;
ALTER INDEX executor_run_task_id_idx RENAME TO agent_run_task_id_idx;

-- 回滚外键约束改名
ALTER TABLE finding RENAME CONSTRAINT finding_executor_run_id_fkey TO finding_agent_id_fkey;
ALTER TABLE llm_invocation RENAME CONSTRAINT llm_invocation_executor_run_id_fkey TO llm_invocation_agent_id_fkey;
ALTER TABLE tool_invocation RENAME CONSTRAINT tool_invocation_executor_run_id_fkey TO tool_invocation_agent_id_fkey;
ALTER TABLE agent_traffic RENAME CONSTRAINT agent_traffic_executor_run_id_fkey TO agent_traffic_agent_id_fkey;
ALTER TABLE scenario RENAME CONSTRAINT scenario_solo_executor_id_fkey TO scenario_solo_agent_id_fkey;
