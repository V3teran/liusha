-- 0055: agent_task_id FK 列重命名为 hunter_id。
-- 跟 0054 表名 rename 配套——表已是 hunter，引用此表的 FK 列也应叫 hunter_id 而非 agent_task_id。
--
-- 涉及 3 个表的 FK 列（都指向 hunter.id）：
--   - finding.agent_task_id          → hunter_id（写 finding 的 hunter run id）
--   - llm_invocation.agent_task_id   → hunter_id（产生这次 LLM 调用的 hunter run id）
--   - tool_invocation.agent_task_id  → hunter_id（产生这次工具调用的 hunter run id）
--
-- 同步改：Go SQL queries / struct JSON tag / 外部 API / viewer / log key 全切。

ALTER TABLE finding RENAME COLUMN agent_task_id TO hunter_id;
ALTER TABLE llm_invocation RENAME COLUMN agent_task_id TO hunter_id;
ALTER TABLE tool_invocation RENAME COLUMN agent_task_id TO hunter_id;
