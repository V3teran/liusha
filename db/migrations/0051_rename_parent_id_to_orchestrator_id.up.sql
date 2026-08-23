-- 0051: agent_task.parent_id → planner_id 列改名。
--
-- v1.1 四角色拆分后：parent task 严格 = planner，child task 严格 = exploitation，
-- parent_id 这种通用关系字段失去存在意义——planner_id 信息密度更高、语义更精准。
--
-- 字段语义：
--   - traffic-analysis（passive 单 agent）   → planner_id IS NULL（无父）
--   - planner（active 父）        → planner_id IS NULL（自己就是根）
--   - exploitation（active 子）          → planner_id = 派出它的 planner.id

-- 1. 列改名（同步 partial index 名）
ALTER TABLE agent_task RENAME COLUMN parent_id TO planner_id;
ALTER INDEX agent_run_parent_id_idx RENAME TO agent_task_planner_id_idx;

COMMENT ON COLUMN agent_task.planner_id IS
    'exploitation 的派出方 planner.id；traffic-analysis / planner 自身为 NULL。'
    ' 替代旧的 parent_id 通用关系字段（v1.1 全改）。';
