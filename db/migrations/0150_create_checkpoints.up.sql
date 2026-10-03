-- checkpoints：framework Checkpointer 的持久化层（ReAct 迭代级状态快照）。
-- 此前 checkpointer 读写该表但无 DDL（schema 缺口）——saveCheckpoint 静默失败。
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
