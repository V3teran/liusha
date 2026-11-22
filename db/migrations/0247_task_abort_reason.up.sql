-- 任务的终止原因交给界面展示。
--
-- task.error_message 只在任务终止时写入，内容是终止原因（「用户手动停止」「任务运行超时」
-- 「重投次数达上限」……），不都是错误；此前没有读取方，任务页看不到任务为什么终止。改名
-- abort_reason，由任务摘要带出、任务详情展示。
-- 目标主机的部分索引没有查询使用（概览按目标主机聚合全部任务，不按主机查找），删除。
ALTER TABLE task RENAME COLUMN error_message TO abort_reason;
COMMENT ON COLUMN task.abort_reason IS '任务终止（aborted）的原因；进行中与已完成的任务为空，续接重开时清空';
DROP INDEX IF EXISTS task_host_idx;
