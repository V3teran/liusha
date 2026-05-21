-- 0027 回滚：重建 flow_facts 表（参考当前 schema 复刻；数据已丢）
CREATE TABLE flow_facts (
    id                   bigserial   PRIMARY KEY,
    engagement_id        uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    flow_id              bigint      REFERENCES http_flow(id) ON DELETE SET NULL,
    agent_run_id         uuid        REFERENCES agent_run(id)  ON DELETE SET NULL,
    operation            text,
    resource_scope       text,
    param_locations      text[],
    carries_auth         boolean,
    credential_locations jsonb,
    reasoning            text,
    created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX flow_facts_engagement_idx ON flow_facts (engagement_id);
CREATE INDEX flow_facts_flow_idx       ON flow_facts (flow_id);
