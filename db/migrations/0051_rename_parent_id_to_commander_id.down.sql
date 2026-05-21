-- 0051 down: 反向 commander_id → parent_id。

ALTER TABLE agent_task RENAME COLUMN commander_id TO parent_id;
ALTER INDEX agent_task_commander_id_idx RENAME TO agent_run_parent_id_idx;

COMMENT ON COLUMN agent_task.parent_id IS NULL;
