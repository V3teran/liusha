-- 0007: 给 agent_task 补一个 created_at 字段。
-- 原 schema 只有 started_at/ended_at，列表排序只能依赖 ctid，不稳定。
-- 新增 created_at 默认 now()，用作任务创建时间和 ListTasks 排序键。
ALTER TABLE agent_task
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();

CREATE INDEX agent_task_created_at_idx ON agent_task (window_id, created_at DESC);
