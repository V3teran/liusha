-- 0195: task 表加 paused 标记——任务行「暂停中」状态可见化。
--
-- pause/resume 原为控制平面内存态（循环侧 detector.Pause），无持久标记——
-- 任务列表无法显示「已暂停」，前端操作列只能双键并列盲点。此列由循环侧
-- 消费 pause/resume 事件时写入；reaper 判死与终态迁移照常生效（paused
-- 不改变 status，仅是 active 的子状态）。

ALTER TABLE task ADD COLUMN paused boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN task.paused IS '暂停中（pause 控制事件生效、未 resume）；仅 status=active 时有意义';
