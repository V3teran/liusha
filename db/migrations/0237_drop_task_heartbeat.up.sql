-- 0237 删除任务心跳列。
--
-- 心跳回收器只看 heartbeat_at 判死，不看任务有没有存活的 run，也不知道整套部署是否停过：停机超过判死阈值
-- 后重启，它与恢复监控同时触发、抢先把本该续跑的任务判为 aborted。run 的停摆与搁浅由恢复监控按 agent_run
-- 与 LLM / 工具活动判定，回收器已移除，这一列没有读取方。
DROP INDEX IF EXISTS task_heartbeat_idx;
ALTER TABLE task DROP COLUMN IF EXISTS heartbeat_at;
