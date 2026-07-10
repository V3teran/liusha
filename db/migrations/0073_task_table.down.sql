-- 0073 down: 删 task 表（旧 active_scan / passive_session 由各自 down 迁移重建）。
DROP TABLE IF EXISTS task;
