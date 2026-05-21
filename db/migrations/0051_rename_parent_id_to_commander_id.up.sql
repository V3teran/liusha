-- 0051: agent_task.parent_id → commander_id 列改名。
--
-- v1.1 四角色拆分后：parent task 严格 = commander，child task 严格 = striker，
-- parent_id 这种通用关系字段失去存在意义——commander_id 信息密度更高、语义更精准。
--
-- 字段语义：
--   - tracker（passive 单 agent）   → commander_id IS NULL（无父）
--   - commander（active 父）        → commander_id IS NULL（自己就是根）
--   - striker（active 子）          → commander_id = 派出它的 commander.id

-- 1. 列改名（同步 partial index 名）
ALTER TABLE agent_task RENAME COLUMN parent_id TO commander_id;
ALTER INDEX agent_run_parent_id_idx RENAME TO agent_task_commander_id_idx;

COMMENT ON COLUMN agent_task.commander_id IS
    'striker 的派出方 commander.id；tracker / commander 自身为 NULL。'
    ' 替代旧的 parent_id 通用关系字段（v1.1 全改）。';
