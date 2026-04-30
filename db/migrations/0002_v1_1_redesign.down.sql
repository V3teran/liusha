CREATE TABLE traffic_window (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    flows         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    status        text        NOT NULL CHECK (status IN ('open','closed','consumed')),
    started_at    timestamptz NOT NULL DEFAULT now(),
    closed_at     timestamptz
);
CREATE INDEX traffic_window_engagement_status_idx
    ON traffic_window (engagement_id, status, started_at);

ALTER TABLE agent_task ADD COLUMN parent_task_id uuid REFERENCES agent_task(id) ON DELETE SET NULL;
