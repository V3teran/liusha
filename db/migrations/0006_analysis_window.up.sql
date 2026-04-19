CREATE TABLE analysis_window (
    id             uuid PRIMARY KEY,
    engagement_id  uuid NOT NULL REFERENCES engagement(id),
    trigger_kind   text NOT NULL
                   CHECK (trigger_kind IN ('time','flow_count','manual')),
    flow_count     int,
    started_at     timestamptz NOT NULL DEFAULT now(),
    ended_at       timestamptz
);
CREATE INDEX ON analysis_window (engagement_id, started_at DESC);
