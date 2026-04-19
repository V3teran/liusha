-- 0003: agent_task 记录单个 AI agent 在某个 analysis_window 内执行的一次任务——
-- 三级生命周期 Engagement → AnalysisWindow → AgentTask 的叶子层。
-- input_node_ids / output_node_ids 把 Attack Graph 节点显式绑定到任务，保证溯源。
-- cost_micro_usd 使用 bigint 微美元（1e-6 USD），避免 float 精度丢失。
CREATE TABLE agent_task (
    id               uuid PRIMARY KEY,
    window_id        uuid NOT NULL REFERENCES analysis_window(id) ON DELETE CASCADE,
    role             text NOT NULL,
    status           text NOT NULL
                     CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
    input_node_ids   uuid[] NOT NULL DEFAULT '{}',
    output_node_ids  uuid[] NOT NULL DEFAULT '{}',
    cost_micro_usd   bigint NOT NULL DEFAULT 0,
    started_at       timestamptz,
    ended_at         timestamptz
);

-- 按窗口取任务清单 + 状态过滤（看板/进度页）。
CREATE INDEX agent_task_window_idx ON agent_task (window_id, status);
-- 按角色看任务状态（成本/质量回溯）。
CREATE INDEX agent_task_role_idx ON agent_task (role, status);
