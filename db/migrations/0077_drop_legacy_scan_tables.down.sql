-- 0077 down: 重建 active_scan + passive_session（对齐 0072 后的最终累积形态）。
-- 清库前提，不回填数据；count 列已在 0042 删除，此处不重建。

-- ── active_scan（0038 建 + 0063 status completed + 0070 paused_ms + 0072 heartbeat）
CREATE TABLE active_scan (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    brief         text NOT NULL,
    target_host   text,
    status        text NOT NULL DEFAULT 'active'
                  CHECK (status IN ('active','aborted','completed')),
    ended_at      timestamptz,
    error_message text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    paused_ms     bigint NOT NULL DEFAULT 0,
    heartbeat_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX active_scan_status_idx ON active_scan (status, created_at DESC);
CREATE INDEX active_scan_heartbeat_idx ON active_scan (status, heartbeat_at) WHERE status = 'active';

-- ── passive_session（0038 建 + 0068 conversation_id + 0072 heartbeat）
CREATE TABLE passive_session (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host            text NOT NULL,
    status          text NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','archived','aborted')),
    expires_at      timestamptz NOT NULL,
    ended_at        timestamptz,
    error_message   text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    conversation_id text NOT NULL DEFAULT '',
    heartbeat_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX passive_session_active_host_uniq
    ON passive_session (host) WHERE status = 'active';
CREATE INDEX passive_session_status_idx ON passive_session (status, created_at DESC);
CREATE INDEX passive_session_heartbeat_idx ON passive_session (status, heartbeat_at) WHERE status = 'active';
