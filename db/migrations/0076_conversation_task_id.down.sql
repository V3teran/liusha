-- 0076 down: conversation.task_id → scan_id（恢复 active_scan FK）。
-- 清库前提，不回填；对齐 0066 建表形态。
DROP INDEX IF EXISTS conversation_task_idx;
ALTER TABLE conversation DROP COLUMN IF EXISTS task_id;
ALTER TABLE conversation ADD COLUMN scan_id uuid REFERENCES active_scan(id) ON DELETE SET NULL;
CREATE INDEX conversation_scan_idx ON conversation (scan_id) WHERE scan_id IS NOT NULL;
