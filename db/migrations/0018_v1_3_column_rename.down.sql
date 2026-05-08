-- 0018 down: 回滚 0018 列名整改
ALTER TABLE http_flow RENAME COLUMN created_at TO ts;
ALTER TABLE engagement RENAME COLUMN agent_run_count TO react_run_count;

ALTER TABLE llm_invocation RENAME COLUMN error_message TO error;
ALTER INDEX llm_invocation_agent_run_idx RENAME TO llm_call_task_idx;
ALTER TABLE llm_invocation RENAME CONSTRAINT llm_invocation_agent_run_id_fkey TO llm_call_task_id_fkey;
ALTER TABLE llm_invocation RENAME COLUMN agent_run_id TO task_id;

ALTER TABLE finding RENAME CONSTRAINT finding_agent_run_id_fkey TO finding_task_id_fkey;
ALTER TABLE finding RENAME COLUMN agent_run_id TO task_id;

ALTER TABLE flow_facts RENAME CONSTRAINT flow_facts_agent_run_id_fkey TO flow_decision_task_id_fkey;
ALTER TABLE flow_facts RENAME COLUMN agent_run_id TO task_id;
