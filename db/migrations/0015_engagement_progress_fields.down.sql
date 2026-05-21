-- 0015 down: 撤销 engagement 进度 / 起止 / 错误字段。
ALTER TABLE engagement
    DROP COLUMN ended_at,
    DROP COLUMN error_message,
    DROP COLUMN flow_count,
    DROP COLUMN finding_count,
    DROP COLUMN react_run_count;
