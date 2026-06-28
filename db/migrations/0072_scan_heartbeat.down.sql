-- 回滚 0072：去 heartbeat_at 列 + 索引。
DROP INDEX IF EXISTS active_scan_heartbeat_idx;
DROP INDEX IF EXISTS passive_session_heartbeat_idx;
ALTER TABLE active_scan DROP COLUMN IF EXISTS heartbeat_at;
ALTER TABLE passive_session DROP COLUMN IF EXISTS heartbeat_at;
