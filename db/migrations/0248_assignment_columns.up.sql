-- 批次只是一次下发的分组：删去只写不读的下发清单与描述。
--
-- payload 是下发清单的原文（人工下发的目标、代理捕获的主机），下发的内容已在各任务的目标
-- 与目标主机里，清单没有任何读取方。description 由「新建任务」采集，立即下发的批次在任何界面
-- 都不展示它；描述只对定时模板有意义（模板列表展示），那里另有 cron_schedule.description。
ALTER TABLE assignment DROP COLUMN payload;
ALTER TABLE assignment DROP COLUMN description;
