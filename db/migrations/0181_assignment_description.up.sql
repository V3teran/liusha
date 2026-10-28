-- 0181: assignment / cron_schedule 增补 description（批次描述）。
--
-- 任务模块（前端「新建任务」弹层）采集批次名称 + 批次描述：名称落既有 title，
-- 描述此前无落点。assignment（即时下发）与 cron_schedule（定时模板，fireOne 克隆
-- 进新 assignment 时携带）两表同步加列，语义一致：可空的长文本，纯展示，无约束。

ALTER TABLE assignment    ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
ALTER TABLE cron_schedule ADD COLUMN IF NOT EXISTS description text NOT NULL DEFAULT '';
