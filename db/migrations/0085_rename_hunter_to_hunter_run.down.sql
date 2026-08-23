-- 0085 down: agent_run → agent，逐条反向 rename（还原 0084 后的真实对象名）。
ALTER TABLE agent_run RENAME CONSTRAINT agent_run_task_id_fkey         TO agent_task_id_fkey;
ALTER TABLE agent_run RENAME CONSTRAINT agent_run_planner_id_fkey TO agent_planner_id_fkey;
ALTER TABLE agent_run RENAME CONSTRAINT agent_run_status_check         TO agent_status_check;
ALTER TABLE agent_run RENAME CONSTRAINT agent_run_role_check           TO agent_role_check;

ALTER INDEX agent_run_task_idx             RENAME TO agent_task_idx;
ALTER INDEX agent_run_planner_id_idx  RENAME TO agent_planner_id_idx;
ALTER INDEX agent_run_pkey                 RENAME TO agent_pkey;

ALTER TABLE agent_run RENAME TO agent;
