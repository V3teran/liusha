-- 0055 down: 回滚 agent_id → agent_task_id。

ALTER TABLE tool_invocation RENAME COLUMN agent_id TO agent_task_id;
ALTER TABLE llm_invocation RENAME COLUMN agent_id TO agent_task_id;
ALTER TABLE finding RENAME COLUMN agent_id TO agent_task_id;
