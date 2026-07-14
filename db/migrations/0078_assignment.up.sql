-- 0078: assignment 下发容器 + cron_schedule 定时模板（P1）。
--
-- 设计（见 spec §3）：一切下发皆走 assignment——api 单发/批量、聚合器自动攒批，
-- 后端统一"建 assignment → 展开 task"。task.assignment_id NOT NULL 强外键，无孤儿 task。
--
--   assignment：一次性下发实例，无 status 列（整体状态由子 task 聚合派生，读时算）。
--   cron_schedule：定时模板（P1 只建空表，Scheduler 逻辑留 P4）。此处先建是为让
--     assignment.schedule_id 外键当场成立，避免 P4 再 ALTER 改表两次。
--
-- 存量可丢（spec §9）：task 已存在（P0 建，无 assignment_id）。空库无搬运负担——
-- 先清 task（CASCADE 连带清 hunter/finding/... 的 task_id 引用），再加 NOT NULL 列。

-- ── cron_schedule（定时模板，学 K8s CronJob；P1 只建表）
CREATE TABLE cron_schedule (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode         text NOT NULL CHECK (mode IN ('active','passive')),
    cron_expr    text NOT NULL,               -- 标准 5 段 cron
    payload      jsonb NOT NULL DEFAULT '[]', -- 触发时克隆进新 assignment 的清单
    title        text NOT NULL DEFAULT '',
    enabled      boolean NOT NULL DEFAULT true,
    next_run_at  timestamptz,
    last_run_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cron_schedule_due_idx ON cron_schedule (next_run_at) WHERE enabled;

-- ── assignment（一次性下发实例）
CREATE TABLE assignment (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    mode         text NOT NULL CHECK (mode IN ('active','passive')),
    source       text NOT NULL CHECK (source IN ('manual','auto')),  -- 谁发：人工/聚合器
    payload      jsonb NOT NULL DEFAULT '[]',  -- active=[{brief,host}...]；passive=[{flow_id...}]
    title        text NOT NULL DEFAULT '',
    schedule_id  uuid REFERENCES cron_schedule(id) ON DELETE SET NULL,  -- 定时克隆来源（手动为 NULL）
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX assignment_schedule_idx ON assignment (schedule_id) WHERE schedule_id IS NOT NULL;

-- ── task 加 assignment_id（NOT NULL 强外键）
DELETE FROM task;  -- 清 P0/e2e 残留（CASCADE 连带清引用行），空库前提下加 NOT NULL 列
ALTER TABLE task ADD COLUMN assignment_id uuid NOT NULL REFERENCES assignment(id) ON DELETE CASCADE;
CREATE INDEX task_assignment_idx ON task (assignment_id);

COMMENT ON TABLE assignment IS '一次性下发实例（一切下发皆走此，展开 task）；无 status 列，整体态由子 task 派生';
COMMENT ON TABLE cron_schedule IS '定时模板（P1 空表，P4 加 Scheduler）；每次触发克隆一个 assignment';
COMMENT ON COLUMN task.assignment_id IS '所属 assignment（NOT NULL 强外键，无孤儿 task）';
