-- 回滚：删 triage 处置字段 + 状态索引
DROP INDEX IF EXISTS finding_status_idx;

ALTER TABLE finding
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS triage_note,
    DROP COLUMN IF EXISTS triaged_at;
