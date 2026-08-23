-- 0075 down: 拆表回滚 —— 删 proxy_traffic / agent_traffic，重建统一 http_flow。
-- 清库前提，不搬运数据；列结构对齐 0060/0062/0065 累积后的 http_flow 形态。

DROP TABLE IF EXISTS agent_traffic;
DROP TABLE IF EXISTS proxy_traffic;

CREATE TABLE http_flow (
    id               bigserial PRIMARY KEY,
    owner_type       text NOT NULL CHECK (owner_type IN ('passive_session','active_scan')),
    owner_id         uuid NOT NULL,
    agent_id        uuid REFERENCES agent(id) ON DELETE SET NULL,
    source           text NOT NULL DEFAULT 'external' CHECK (source IN ('external','internal')),
    identity         text,
    tool             text,
    host             text NOT NULL,
    method           text NOT NULL,
    url              text NOT NULL,
    path             text NOT NULL,
    request_headers  jsonb,
    request_body     bytea,
    status_code      int NOT NULL DEFAULT 0,
    response_headers jsonb,
    response_body    bytea,
    duration_ms      int NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX http_flow_owner_host_idx ON http_flow (owner_id, host, created_at DESC);
CREATE INDEX http_flow_agent_idx     ON http_flow (agent_id) WHERE agent_id IS NOT NULL;
CREATE INDEX http_flow_source_idx     ON http_flow (source, created_at DESC);
CREATE INDEX http_flow_path_idx       ON http_flow (host, path);

-- 恢复 finding.source_flow_id → http_flow 外键（与 up 去 FK 对称）
ALTER TABLE finding ADD CONSTRAINT finding_source_flow_id_fkey
    FOREIGN KEY (source_flow_id) REFERENCES http_flow(id) ON DELETE SET NULL;
