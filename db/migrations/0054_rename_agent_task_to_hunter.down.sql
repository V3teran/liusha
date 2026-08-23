-- 0054 down: 回滚 agent → agent_task。
-- 同步反向 rename 3 个 index + 4 个 constraint。

ALTER TABLE agent RENAME CONSTRAINT agent_planner_id_fkey TO agent_task_planner_id_fkey;
ALTER TABLE agent RENAME CONSTRAINT agent_role_check TO agent_task_role_check;
ALTER TABLE agent RENAME CONSTRAINT agent_status_check TO agent_run_status_check;
ALTER TABLE agent RENAME CONSTRAINT agent_owner_type_check TO agent_run_owner_type_check;

ALTER INDEX agent_owner_idx RENAME TO agent_task_owner_idx;
ALTER INDEX agent_planner_id_idx RENAME TO agent_task_planner_id_idx;
ALTER INDEX agent_pkey RENAME TO agent_run_pkey;

ALTER TABLE agent RENAME TO agent_task;
