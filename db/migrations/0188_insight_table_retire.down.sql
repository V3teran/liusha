-- 逆迁移：按退役前的表结构重建空表（数据不可逆——退役时未搬运）。
CREATE TABLE insight (
    id                  TEXT PRIMARY KEY,
    assignment_id       TEXT NOT NULL,
    category            TEXT NOT NULL,
    priority            TEXT NOT NULL DEFAULT 'medium',
    confidence          TEXT NOT NULL DEFAULT 'possible',
    severity            TEXT,
    summary             TEXT NOT NULL,
    body                TEXT DEFAULT '',
    tags                JSONB DEFAULT '[]',
    source_task_id      TEXT,
    source_agent_run_id TEXT,
    related_nodes       JSONB DEFAULT '[]',
    created_at          TIMESTAMPTZ DEFAULT now(),
    updated_at          TIMESTAMPTZ DEFAULT now()
);
