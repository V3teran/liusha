-- 0055: agent_task_id FK 列重命名为 agent_id。
-- 跟 0054 表名 rename 配套——表已是 agent，引用此表的 FK 列也应叫 agent_id 而非 agent_task_id。
--
-- 涉及 3 个表的 FK 列（都指向 agent.id）：
--   - finding.agent_task_id          → agent_id（写 finding 的 agent run id）
--   - llm_invocation.agent_task_id   → agent_id（产生这次 LLM 调用的 agent run id）
--   - tool_invocation.agent_task_id  → agent_id（产生这次工具调用的 agent run id）
--
-- 同步改：Go SQL queries / struct JSON tag / 外部 API / 前端 / log key 全切。

ALTER TABLE finding RENAME COLUMN agent_task_id TO agent_id;
ALTER TABLE llm_invocation RENAME COLUMN agent_task_id TO agent_id;
ALTER TABLE tool_invocation RENAME COLUMN agent_task_id TO agent_id;
