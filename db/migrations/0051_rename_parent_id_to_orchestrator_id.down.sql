-- 0051 down: 反向 orchestrator_id → parent_id。

ALTER TABLE agent_task RENAME COLUMN orchestrator_id TO parent_id;
ALTER INDEX agent_task_orchestrator_id_idx RENAME TO agent_run_parent_id_idx;

COMMENT ON COLUMN agent_task.parent_id IS NULL;
