-- 0122: 创建 leads 表，按 assignment 隔离情报黑板
--
-- 设计决策：
-- 1. 按 assignment_id 隔离（同一批测试共享情报）
-- 2. PostgreSQL 持久化（废弃 Redis）
-- 3. 支持 3 种 kind：clue/observation/deadend

CREATE TABLE IF NOT EXISTS leads (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('clue', 'observation', 'deadend')),
    detail text NOT NULL,
    executor_id text,
    source_task_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_leads_assignment_created ON leads(assignment_id, created_at DESC);

COMMENT ON TABLE leads IS '情报黑板：assignment 级别的跨 task 情报共享';
COMMENT ON COLUMN leads.assignment_id IS '所属 assignment（隔离边界）';
COMMENT ON COLUMN leads.kind IS 'clue=可疑点待验证 / observation=既成发现 / deadend=死路绕开';
COMMENT ON COLUMN leads.detail IS '一句人话描述，位置/细节都在这里';
COMMENT ON COLUMN leads.executor_id IS '产出情报的 executor（可选）';
COMMENT ON COLUMN leads.source_task_id IS '产出情报的 task.id（溯源）';
