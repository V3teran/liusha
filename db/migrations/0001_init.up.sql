CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE engagement (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       text        NOT NULL DEFAULT 'default',
    mode            text        NOT NULL CHECK (mode IN ('proxy','browser')),
    scope_host      text        NOT NULL,
    status          text        NOT NULL CHECK (status IN ('active','aborted','archived')),
    memory_facts    jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- {evidence:[], boundaries:[]}
    memory_ideas    jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- {hypotheses:[{direction,status,ts}]}
    memory_hints    jsonb       NOT NULL DEFAULT '{}'::jsonb,  -- {hints:[{from_skill,content,priority,ts}]}
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_activity_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX engagement_active_uniq
    ON engagement (tenant_id, scope_host)
    WHERE status = 'active';

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

CREATE TABLE agent_task (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id   uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    parent_task_id  uuid        REFERENCES agent_task(id) ON DELETE SET NULL,
    role            text        NOT NULL,
    skill           text        NOT NULL DEFAULT '',
    input           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    budget          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    result          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status          text        NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','running','done','aborted','error')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_task_engagement_idx ON agent_task (engagement_id, created_at);

CREATE TABLE graph_node (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    dedup_key     text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX graph_node_uniq ON graph_node (engagement_id, kind, dedup_key);

CREATE TABLE graph_edge (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    from_id       uuid        NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    to_id         uuid        NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX graph_edge_uniq ON graph_edge (engagement_id, from_id, to_id, kind);

CREATE TABLE finding (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    task_id       uuid        REFERENCES agent_task(id) ON DELETE SET NULL,
    kind          text        NOT NULL,
    severity      text        NOT NULL CHECK (severity IN ('info','low','medium','high','critical')),
    title         text        NOT NULL,
    target        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    evidence      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    tool          text        NOT NULL DEFAULT '',
    confidence    text        NOT NULL DEFAULT 'unverified'
                  CHECK (confidence IN ('unverified','verified','rejected')),
    dedup_key     text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX finding_uniq ON finding (engagement_id, dedup_key);

CREATE TABLE http_flow (
    id              bigserial   PRIMARY KEY,
    engagement_id   uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    ts              timestamptz NOT NULL DEFAULT now(),
    method          text        NOT NULL,
    url             text        NOT NULL,
    request_headers jsonb       NOT NULL DEFAULT '{}'::jsonb,
    request_body    bytea,
    request_truncated boolean   NOT NULL DEFAULT false,
    status_code     int,
    response_headers jsonb      NOT NULL DEFAULT '{}'::jsonb,
    response_body   bytea,
    response_truncated boolean  NOT NULL DEFAULT false
);
CREATE INDEX http_flow_engagement_ts_idx ON http_flow (engagement_id, ts);

CREATE TABLE llm_call (
    id            bigserial   PRIMARY KEY,
    task_id       uuid        REFERENCES agent_task(id) ON DELETE SET NULL,
    engagement_id uuid        REFERENCES engagement(id) ON DELETE SET NULL,
    provider      text        NOT NULL,
    model         text        NOT NULL,
    in_tokens     int         NOT NULL DEFAULT 0,
    out_tokens    int         NOT NULL DEFAULT 0,
    cached_tokens int         NOT NULL DEFAULT 0,
    cost_usd      numeric(12,6) NOT NULL DEFAULT 0,
    latency_ms    int         NOT NULL DEFAULT 0,
    finish_reason text        NOT NULL DEFAULT '',
    error         text        NOT NULL DEFAULT '',
    role          text        NOT NULL DEFAULT '',  -- T21 RouteKey（react.main / reviewer / distill / compaction / vision），便于按 role 统计成本
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_call_task_idx ON llm_call (task_id);
CREATE INDEX llm_call_engagement_idx ON llm_call (engagement_id, created_at);
