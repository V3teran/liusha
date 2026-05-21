-- 0054 down: 回滚 hunter → agent_task。
-- 同步反向 rename 3 个 index + 4 个 constraint。

ALTER TABLE hunter RENAME CONSTRAINT hunter_commander_id_fkey TO agent_task_commander_id_fkey;
ALTER TABLE hunter RENAME CONSTRAINT hunter_role_check TO agent_task_role_check;
ALTER TABLE hunter RENAME CONSTRAINT hunter_status_check TO agent_run_status_check;
ALTER TABLE hunter RENAME CONSTRAINT hunter_owner_type_check TO agent_run_owner_type_check;

ALTER INDEX hunter_owner_idx RENAME TO agent_task_owner_idx;
ALTER INDEX hunter_commander_id_idx RENAME TO agent_task_commander_id_idx;
ALTER INDEX hunter_pkey RENAME TO agent_run_pkey;

ALTER TABLE hunter RENAME TO agent_task;
