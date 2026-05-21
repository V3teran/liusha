-- 0050: agent_task.role 切换到 tracker/commander/striker 三角色 enum + CHECK 约束。
--
-- 历史：0001 ~ 0049 期间 agent_task.role 都写 "hunter"（worker.RoleHunter 字面值），
-- 跟 asynq queue 路由 enum 耦合在同一字段。
-- v1.1: agent 角色拆 tracker（passive 单 agent）/ commander（active 父）/ striker（active 子），
-- worker.Role 改名 RoleAgent（asynq queue 标识，跟 agent 角色解耦）。
--
-- 此 migration：
--   1. 旧数据 backfill：role='hunter' 按 (owner_type, parent_id) 反推：
--      - owner_type='passive_session'                       → 'tracker'
--      - owner_type='active_scan' + parent_id IS NULL        → 'commander'
--      - owner_type='active_scan' + parent_id IS NOT NULL    → 'striker'
--   2. 加 CHECK 约束限制三值。

-- 1. backfill 旧 hunter 行
UPDATE agent_task SET role = 'tracker'
    WHERE role = 'hunter' AND owner_type = 'passive_session';

UPDATE agent_task SET role = 'striker'
    WHERE role = 'hunter' AND owner_type = 'active_scan' AND parent_id IS NOT NULL;

UPDATE agent_task SET role = 'commander'
    WHERE role = 'hunter' AND owner_type = 'active_scan' AND parent_id IS NULL;

-- 2. 加 CHECK 约束（三值枚举）
ALTER TABLE agent_task
    ADD CONSTRAINT agent_task_role_check
    CHECK (role IN ('tracker', 'commander', 'striker'));

COMMENT ON COLUMN agent_task.role IS
    'agent 角色枚举：tracker（passive 单 agent）/ commander（active 父）/ striker（active 子）。跟 asynq queue 路由（worker.RoleAgent）解耦。';
