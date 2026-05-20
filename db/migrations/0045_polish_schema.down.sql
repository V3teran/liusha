-- 0045 down: 反向恢复各项。

-- 4. finding 元数据字段
ALTER TABLE finding DROP COLUMN remediation;
ALTER TABLE finding DROP COLUMN first_seen_at;
ALTER TABLE finding DROP COLUMN owasp_category;
ALTER TABLE finding DROP COLUMN cwe_id;

-- 3. http_flow.host
DROP INDEX IF EXISTS http_flow_host_idx;
ALTER TABLE http_flow DROP COLUMN host;

-- 2. active_scan.target_host 回到 nullable
ALTER TABLE active_scan ALTER COLUMN target_host DROP NOT NULL;
ALTER TABLE active_scan ALTER COLUMN target_host DROP DEFAULT;

-- 1. status check 恢复 'archived'
ALTER TABLE passive_session DROP CONSTRAINT passive_session_status_check;
ALTER TABLE passive_session ADD CONSTRAINT passive_session_status_check
    CHECK (status IN ('active','archived','aborted'));
ALTER TABLE active_scan DROP CONSTRAINT active_scan_status_check;
ALTER TABLE active_scan ADD CONSTRAINT active_scan_status_check
    CHECK (status IN ('active','archived','aborted'));
