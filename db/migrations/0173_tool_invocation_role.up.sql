-- tool_invocation 增加 role 列：工具调用的 agent 归属（planner/executor/evaluator/monitor）。
-- 此前整任务的工具审计统一挂 runs[0]，跨 agent 行为审计不可用（“planner 写了 7 条
-- write_insight”是归因假象——实际写者是 executor）。
ALTER TABLE tool_invocation
    ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT '';
