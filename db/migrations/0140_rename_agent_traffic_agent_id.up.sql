-- agent_traffic.agent_id → agent_run_id：与代码层命名对齐（记录产生流量的 agent 执行）。
ALTER TABLE agent_traffic RENAME COLUMN agent_id TO agent_run_id;
