-- 0043 down: 反向 rename agent_task → agent_run。

ALTER TABLE agent_task RENAME TO agent_run;
ALTER INDEX agent_task_owner_idx RENAME TO agent_run_owner_idx;
