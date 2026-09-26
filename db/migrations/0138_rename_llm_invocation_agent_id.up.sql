-- llm_invocation.agent_id → agent_run_id：与代码层命名对齐（记录发起调用的 agent 执行）。
ALTER TABLE llm_invocation RENAME COLUMN agent_id TO agent_run_id;
