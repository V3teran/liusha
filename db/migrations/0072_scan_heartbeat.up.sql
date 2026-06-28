-- 0072: active_scan + passive_session 加 heartbeat_at（B2 心跳探活）。
-- agent 每次工具调用刷新此列；后台 reaper 发现心跳超时（进程崩溃/卡死）即判 aborted，
-- 修复「scanner 进程死 → 扫描永久卡 active、前端永远显示进行中」缺口。
-- DEFAULT now()：既有行回填当前时间，配合 reaper 的启动宽限不会被误杀。

ALTER TABLE active_scan ADD COLUMN heartbeat_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE passive_session ADD COLUMN heartbeat_at timestamptz NOT NULL DEFAULT now();

-- reaper 巡逻按 (status, heartbeat_at) 过滤活跃且心跳老的行。
CREATE INDEX active_scan_heartbeat_idx ON active_scan (status, heartbeat_at) WHERE status = 'active';
CREATE INDEX passive_session_heartbeat_idx ON passive_session (status, heartbeat_at) WHERE status = 'active';
