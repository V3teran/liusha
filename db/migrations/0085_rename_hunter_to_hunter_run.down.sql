-- 0085 down: hunter_run → hunter，逐条反向 rename（还原 0084 后的真实对象名）。
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_run_task_id_fkey         TO hunter_task_id_fkey;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_run_orchestrator_id_fkey TO hunter_orchestrator_id_fkey;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_run_status_check         TO hunter_status_check;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_run_role_check           TO hunter_role_check;

ALTER INDEX hunter_run_task_idx             RENAME TO hunter_task_idx;
ALTER INDEX hunter_run_orchestrator_id_idx  RENAME TO hunter_orchestrator_id_idx;
ALTER INDEX hunter_run_pkey                 RENAME TO hunter_pkey;

ALTER TABLE hunter_run RENAME TO hunter;
