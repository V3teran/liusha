-- 执行记录去掉没有读取方的列。
--
-- agent_run.role 恒为 planner：一次 run 是整个四 agent 认知循环，四个 agent 共享这一行，「发起
-- 角色」不区分任何东西。agent_run.result 只写不读：完成时的报告与出错时的错误信息没有任何
-- 读取方（停止原因写在时间线终态节点，出错细节记日志，任务因此终止时原因写在任务行）。两列
-- 删除。(task_id) 索引被 (task_id, created_at DESC) 的前缀覆盖，一并删除。
ALTER TABLE agent_run DROP COLUMN role;
ALTER TABLE agent_run DROP COLUMN result;
DROP INDEX IF EXISTS idx_agent_run_task_id;
