-- 0044: 列 rename llm_invocation.call_purpose → role。
--
-- 设计动因：
--   - "call_purpose" 含义模糊：既不是 role 也不是 caller
--   - 实际值是 agent / inspector / react_main / summary —— 这些就是 role
--   - agent_task.role 已经叫 role，命名对齐降低心智负担
--
-- 注意与 OpenAI/Anthropic API 的 message.role (user/assistant/system) 不同：
--   - 这里的 role 指**调用者角色**（哪个 ReAct agent 发起的 call）
--   - 不直接对应 message-level role

ALTER TABLE llm_invocation RENAME COLUMN call_purpose TO role;
