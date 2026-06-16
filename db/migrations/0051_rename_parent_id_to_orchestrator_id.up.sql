-- 0051: agent_task.parent_id → orchestrator_id 列改名。
--
-- v1.1 四角色拆分后：parent task 严格 = orchestrator，child task 严格 = exploitation，
-- parent_id 这种通用关系字段失去存在意义——orchestrator_id 信息密度更高、语义更精准。
--
-- 字段语义：
--   - traffic-analysis（passive 单 agent）   → orchestrator_id IS NULL（无父）
--   - orchestrator（active 父）        → orchestrator_id IS NULL（自己就是根）
--   - exploitation（active 子）          → orchestrator_id = 派出它的 orchestrator.id

-- 1. 列改名（同步 partial index 名）
ALTER TABLE agent_task RENAME COLUMN parent_id TO orchestrator_id;
ALTER INDEX agent_run_parent_id_idx RENAME TO agent_task_orchestrator_id_idx;

COMMENT ON COLUMN agent_task.orchestrator_id IS
    'exploitation 的派出方 orchestrator.id；traffic-analysis / orchestrator 自身为 NULL。'
    ' 替代旧的 parent_id 通用关系字段（v1.1 全改）。';
