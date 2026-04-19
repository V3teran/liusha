-- 0005: http_flow 记录 proxy / browser 模式下抓到的每一条 HTTP 交互，
-- 作为 agent 分析、回放和取证的唯一事实源。
-- 体量大且按时间严格递增，采用 RANGE(ts) 分区避免单表膨胀和 autovacuum 压力。
-- PK 必须包含分区键 ts，所以设为 (id, ts) 组合主键。
CREATE TABLE http_flow (
    id                uuid NOT NULL,
    engagement_id     uuid NOT NULL,
    ts                timestamptz NOT NULL,
    method            text,
    url               text,
    req_content_type  text,
    req_headers       jsonb,
    req_body          bytea,
    resp_status       int,
    resp_content_type text,
    resp_headers      jsonb,
    resp_body         bytea,
    body_truncated    boolean NOT NULL DEFAULT false,
    PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

-- 默认分区：在按月滚动分区 cron 上线前保证写入不失败。
-- 上线后再按月创建 http_flow_YYYYMM 子分区，并周期性归档默认分区。
CREATE TABLE http_flow_default PARTITION OF http_flow DEFAULT;

-- 默认分区上按 engagement 时间倒序查询（回放 / 时间线）。
CREATE INDEX http_flow_default_eng_ts_idx
    ON http_flow_default (engagement_id, ts DESC);
