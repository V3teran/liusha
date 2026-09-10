-- 0124: lead 包全面重构 - 回滚
--
-- 回滚到旧的 lead 表结构

BEGIN;

-- 删除新表
DROP TABLE IF EXISTS lead CASCADE;

-- 恢复旧表
CREATE TABLE IF NOT EXISTS lead (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('clue', 'observation', 'deadend')),
    detail text NOT NULL,
    executor_id text,
    source_task_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX insight(assignment_id, created_at DESC);

COMMENT ON TABLE insight IS '情报黑板：assignment 级别的跨 task 情报共享';
COMMENT ON COLUMN lead.assignment_id IS '所属 assignment（隔离边界）';
COMMENT ON COLUMN lead.kind IS 'clue=可疑点待验证 / observation=既成发现 / deadend=死路绕开';
COMMENT ON COLUMN lead.detail IS '一句人话描述，位置/细节都在这里';
COMMENT ON COLUMN lead.executor_id IS '产出情报的 executor（可选）';
COMMENT ON COLUMN lead.source_task_id IS '产出情报的 task.id（溯源）';

COMMIT;
