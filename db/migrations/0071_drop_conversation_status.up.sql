-- 彻底删除 conversation.status 僵尸字段（默认 active 从不更新，无业务读取；运行态一律派生
-- 自关联 active_scan/passive_session，见代码 RunStatus）。不留遗留：列 + CHECK 约束 + 索引全删。
-- DROP COLUMN 会自动连带 conversation_status_check 约束，索引单独显式删。
DROP INDEX IF EXISTS conversation_status_idx;
ALTER TABLE conversation DROP COLUMN IF EXISTS status;
