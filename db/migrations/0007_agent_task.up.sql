CREATE TABLE agent_task (
    id               uuid PRIMARY KEY,
    window_id        uuid NOT NULL REFERENCES analysis_window(id),
    role             text NOT NULL,
    status           text NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending','running','succeeded','failed','cancelled')),
    input_node_ids   uuid[],
    output_node_ids  uuid[],
    cost_micro_usd   bigint NOT NULL DEFAULT 0,
    started_at       timestamptz,
    ended_at         timestamptz
);
CREATE INDEX ON agent_task (window_id, status);
