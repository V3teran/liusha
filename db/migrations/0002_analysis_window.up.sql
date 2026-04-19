-- 0002: analysis_window 记录一次 engagement 内的"分析窗口"——按时间/流量阈值或人工触发切出的一段窗口，
-- 是三级生命周期 Engagement → AnalysisWindow → AgentTask 的中间层。
CREATE TABLE analysis_window (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    trigger_kind   text NOT NULL CHECK (trigger_kind IN ('time','flow_count','manual')),
    flow_count     int,
    started_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz
);

-- 按 engagement 分组、按时间倒序查询窗口列表。
CREATE INDEX analysis_window_engagement_idx
    ON analysis_window (engagement_id, started_at DESC);
