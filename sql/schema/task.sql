-- sql/schema/task.sql
-- 任务表的 schema（示例）
-- 这是给 sqlc 参考的，实际 schema 在 migrations 中

CREATE TABLE IF NOT EXISTS task (
    id TEXT PRIMARY KEY,
    mode TEXT NOT NULL,
    target_host TEXT NOT NULL,
    target_port INTEGER,
    brief TEXT,
    status TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_task_status ON task(status);
CREATE INDEX idx_task_created_at ON task(created_at DESC);
