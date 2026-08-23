-- dev 种子：为 LLM 审计（llm_invocation）+ 执行图（message 事件流）+ 会话用量（tool_invocation）
-- 灌可视测试数据。三者共用 seed_traffic.sql 已建好的固定 UUID task/conversation（5eed 前缀）。
--
-- message.metadata 的 JSON 结构须与 internal/einoagent.ScanEvent 一致（字段名大小写不敏感匹配，
-- 见 internal/attackgraph/trace.go traceEvent），否则执行图投影会跳过这些事件、图仍是空的。
-- 覆盖事件类型：reasoning（推理）→ tool_call/tool_result（探测）→ insight（agent 自标判断/信号）
-- → spawn（派子代理）→ 循环 → write_finding 工具调用挂上真实 finding id（成果链能挂接）。
--
-- 幂等：agent_run/message 用固定 UUID + ON CONFLICT DO NOTHING；重灌前先清本脚本产出的行。
BEGIN;

-- 清理上一次种子（仅本脚本挂在 seed task 下的行），避免重复累积。
DELETE FROM message WHERE conversation_id IN (
  '5eed0000-0000-0000-0000-000000000004'::uuid, '5eed0000-0000-0000-0000-000000000005'::uuid
);
DELETE FROM llm_invocation WHERE task_id IN (
  '5eed0000-0000-0000-0000-000000000002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid
);
DELETE FROM tool_invocation WHERE task_id IN (
  '5eed0000-0000-0000-0000-000000000002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid
);
DELETE FROM agent_run WHERE task_id IN (
  '5eed0000-0000-0000-0000-000000000002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid
);

-- 1) agent_run：api_task（seed.api.local，completed）下一条 planner run + 一条 exploitation
--    子 run（llm_invocation/tool_invocation 的 agent_id 外键指向这里）。
INSERT INTO agent_run (id, role, task_id, status, planner_id)
VALUES
  ('5eed0000-0000-0000-0000-00000000a001'::uuid, 'planner', '5eed0000-0000-0000-0000-000000000003'::uuid, 'done', NULL),
  ('5eed0000-0000-0000-0000-00000000a002'::uuid, 'exploitation', '5eed0000-0000-0000-0000-000000000003'::uuid, 'done', '5eed0000-0000-0000-0000-00000000a001'::uuid)
ON CONFLICT (id) DO NOTHING;

COMMIT;

-- 2) message：api_task 绑定的会话（conv 5）走一遍完整故事线——
--    planner 推理 → 派 exploitation 子代理 → 子代理推理 → 判断(hypothesis) → 探测(tool_call/result)
--    → 信号(signal) → 再判断 → 探测命中 write_finding → 挂到已有 finding（IDOR 那条，账户接管链首节点）。
-- seq 由 bigserial 自动分配，用 CTE 拿回真实 message.id 供后续 llm_invocation 关联（非必须但语义更完整）。
BEGIN;

INSERT INTO message (id, conversation_id, role, kind, content, metadata) VALUES
  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '开始分析 seed.api.local 认证与授权面',
   '{"Kind":"reasoning","AgentName":"planner","Text":"开始分析 seed.api.local 认证与授权面","InTokens":420,"OutTokens":86,"LatencyMs":1200}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '派发 exploitation：核实用户资料更新接口的越权风险',
   '{"Kind":"spawn","ToolName":"task","AgentName":"planner","Args":"{\"subagent_type\":\"exploitation\",\"description\":\"核实用户资料更新接口的越权风险\"}"}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '怀疑 PUT /v1/users/:id 未校验调用者身份与目标 id 一致性',
   '{"Kind":"reasoning","AgentName":"exploitation","Text":"怀疑 PUT /v1/users/:id 未校验调用者身份与目标 id 一致性","InTokens":310,"OutTokens":64,"LatencyMs":900}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '判断：越权访问控制缺失',
   '{"Kind":"insight","ToolName":"mark_insight","AgentName":"exploitation","Args":"{\"type\":\"hypothesis\",\"text\":\"越权访问控制缺失 - 任意用户资料可被覆写(IDOR)\",\"dead_end\":false}"}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '调用工具 replay_traffic',
   '{"Kind":"tool_call","ToolName":"replay_traffic","CallID":"seed-call-1","AgentName":"exploitation","Args":"{\"traffic_id\":5,\"override\":{\"path\":\"/v1/users/1\"}}"}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'tool', 'event',
   '{"status":200,"body":"{\"id\":1,\"email\":\"attacker@evil.com\",\"updated\":true}"}',
   '{"Kind":"tool_result","ToolName":"replay_traffic","CallID":"seed-call-1","AgentName":"exploitation","Result":"{\"status\":200,\"body\":\"{\\\"id\\\":1,\\\"email\\\":\\\"attacker@evil.com\\\",\\\"updated\\\":true}\"}","DurationMs":180}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '信号：用非本人 token 成功覆写他人资料，200 返回确认漏洞成立',
   '{"Kind":"insight","ToolName":"mark_insight","AgentName":"exploitation","Args":"{\"type\":\"signal\",\"text\":\"用非本人 token 成功覆写他人资料，200 返回确认漏洞成立\",\"dead_end\":false}"}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'assistant', 'event',
   '调用工具 write_finding',
   '{"Kind":"tool_call","ToolName":"write_finding","CallID":"seed-call-2","AgentName":"exploitation","Args":"{\"host\":\"seed.api.local\",\"severity\":\"critical\"}"}'::jsonb),

  (gen_random_uuid(), '5eed0000-0000-0000-0000-000000000005'::uuid, 'tool', 'event',
   '已记录漏洞',
   ('{"Kind":"tool_result","ToolName":"write_finding","CallID":"seed-call-2","AgentName":"exploitation","Result":"{\"id\":\"' ||
    (SELECT id FROM finding WHERE host = 'seed.api.local' AND summary LIKE '越权访问控制缺失%' LIMIT 1) ||
    '\"}","DurationMs":45}')::jsonb);

COMMIT;

-- 3) llm_invocation：与 message 里的 reasoning 事件对应的真实 LLM 调用行（供 LLM 审计页展示）。
--    agent_id 关联 agent_run（审计页可按角色筛选），task_id 关联 api_task。
BEGIN;

INSERT INTO llm_invocation (agent_id, task_id, provider, model, in_tokens, out_tokens, cached_tokens, latency_ms, ttft_ms, is_stream, finish_reason, role, messages, result) VALUES
  ('5eed0000-0000-0000-0000-00000000a001'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid,
   'deepseek', 'deepseek-chat', 420, 86, 0, 1200, 380, false, 'stop', 'planner',
   '[{"role":"system","content":"你是编排智能体"},{"role":"user","content":"分析 seed.api.local"}]'::jsonb,
   '{"content":"开始分析 seed.api.local 认证与授权面","tool_calls":[{"function":{"name":"task"}}]}'::jsonb),

  ('5eed0000-0000-0000-0000-00000000a002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid,
   'deepseek', 'deepseek-chat', 310, 64, 0, 900, 260, false, 'stop', 'exploitation',
   '[{"role":"system","content":"你是利用智能体"},{"role":"user","content":"核实用户资料更新接口的越权风险"}]'::jsonb,
   '{"content":"怀疑 PUT /v1/users/:id 未校验调用者身份与目标 id 一致性","tool_calls":[{"function":{"name":"mark_insight"}}]}'::jsonb),

  ('5eed0000-0000-0000-0000-00000000a002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid,
   'deepseek', 'deepseek-chat', 580, 120, 200, 1450, 410, false, 'stop', 'exploitation',
   '[{"role":"user","content":"继续验证"}]'::jsonb,
   '{"content":"发起 replay_traffic 验证越权","tool_calls":[{"function":{"name":"replay_traffic"}}]}'::jsonb),

  ('5eed0000-0000-0000-0000-00000000a002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid,
   'deepseek', 'deepseek-chat', 260, 40, 0, 700, 0, false, 'error', 'exploitation',
   '[{"role":"user","content":"确认漏洞并落库"}]'::jsonb,
   '{"content":""}'::jsonb);

-- 补一条真实错误信息（error_message 独立列，非 result jsonb 内）。
UPDATE llm_invocation SET error_message = 'rate limit exceeded, retry after 2s'
WHERE task_id = '5eed0000-0000-0000-0000-000000000003'::uuid AND finish_reason = 'error';

COMMIT;

-- 4) tool_invocation：与 message 里的 tool_call/tool_result 对应（供会话用量合计展示）。
BEGIN;

INSERT INTO tool_invocation (agent_id, task_id, tool_name, args, output_size, output_preview, duration_ms, done) VALUES
  ('5eed0000-0000-0000-0000-00000000a002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid,
   'replay_traffic', '{"traffic_id":5,"override":{"path":"/v1/users/1"}}'::jsonb, 62,
   '{"status":200,"body":"{\"id\":1,\"email\":\"attacker@evil.com\"}"}', 180, true),

  ('5eed0000-0000-0000-0000-00000000a002'::uuid, '5eed0000-0000-0000-0000-000000000003'::uuid,
   'write_finding', '{"host":"seed.api.local","severity":"critical"}'::jsonb, 40,
   '已记录漏洞', 45, true);

COMMIT;

-- 摘要
SELECT 'message' AS tbl, count(*) FROM message WHERE conversation_id = '5eed0000-0000-0000-0000-000000000005'::uuid
UNION ALL SELECT 'llm_invocation', count(*) FROM llm_invocation WHERE task_id = '5eed0000-0000-0000-0000-000000000003'::uuid
UNION ALL SELECT 'tool_invocation', count(*) FROM tool_invocation WHERE task_id = '5eed0000-0000-0000-0000-000000000003'::uuid
UNION ALL SELECT 'agent_run', count(*) FROM agent_run WHERE task_id = '5eed0000-0000-0000-0000-000000000003'::uuid;
