-- 0046: tool_invocation 表——记录每次工具调用的可观测性 telemetry。
--
-- 设计动因：
--   - 现在工具调用埋在 llm_invocation.result jsonb 里，无法独立查询
--   - "sqlmap 跑了几次/平均耗时/成功率"这类 telemetry 全部丢失
--   - Pentest 编排器的核心可观测性缺口，必须独立成表

CREATE TABLE tool_invocation (
    id              bigserial   PRIMARY KEY,
    agent_task_id   uuid        NOT NULL REFERENCES agent_task(id) ON DELETE CASCADE,
    owner_type      text        NOT NULL CHECK (owner_type IN ('passive_session','active_scan')),
    owner_id        uuid        NOT NULL,
    tool_name       text        NOT NULL,
    args            jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- output_size 是字节数；output_preview 是首 4KB 文本预览（避免存满 PG 撑爆）。
    output_size     int         NOT NULL DEFAULT 0,
    output_preview  text        NOT NULL DEFAULT '',
    duration_ms     int         NOT NULL DEFAULT 0,
    error_message   text,
    done            boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tool_invocation_task_idx ON tool_invocation (agent_task_id, created_at);
CREATE INDEX tool_invocation_owner_idx ON tool_invocation (owner_type, owner_id, created_at DESC);
CREATE INDEX tool_invocation_name_idx ON tool_invocation (tool_name, created_at DESC);

COMMENT ON TABLE tool_invocation IS '每次 ReAct 工具调用的 telemetry：哪个 tool / 参数 / 耗时 / 输出大小 / 错误';
COMMENT ON COLUMN tool_invocation.output_preview IS 'output 首 4096 字符；完整 output 在 LLM message history 里';
