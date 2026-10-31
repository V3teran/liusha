-- 回滚 0183：忠实重建 0150 的 checkpoints 表（0157 将 id/task_id 统一为 uuid 的
-- 转换随表删除而失效，回滚按 0150 原始 text 形态重建）。
CREATE TABLE IF NOT EXISTS checkpoints (
    id               TEXT PRIMARY KEY,
    task_id          TEXT        NOT NULL,
    state_snapshot   JSONB       NOT NULL,
    phase            TEXT        NOT NULL DEFAULT '',
    component_states JSONB       NOT NULL DEFAULT '{}'::jsonb,
    labels           JSONB       NOT NULL DEFAULT '{}'::jsonb,
    size_bytes       BIGINT      NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_checkpoints_task_created ON checkpoints (task_id, created_at DESC);
