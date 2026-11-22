-- 回到改名之前。
CREATE INDEX task_host_idx ON task (target_host) WHERE target_host <> '';
COMMENT ON COLUMN task.abort_reason IS NULL;
ALTER TABLE task RENAME COLUMN abort_reason TO error_message;
