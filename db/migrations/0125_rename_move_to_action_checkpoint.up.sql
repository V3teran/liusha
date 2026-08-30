-- 0125: 重命名 actor_checkpoint 表的列：move_id → action_id
-- 这是 Move → Action 重构的一部分

-- 1. 删除旧索引
DROP INDEX IF EXISTS actor_checkpoint_scan_move_idx;

-- 2. 重命名列
ALTER TABLE actor_checkpoint RENAME COLUMN move_id TO action_id;

-- 3. 创建新索引
CREATE INDEX actor_checkpoint_task_action_idx ON actor_checkpoint (task_id, action_id);

-- 4. 更新唯一约束（如果存在）
ALTER TABLE actor_checkpoint DROP CONSTRAINT IF EXISTS actor_checkpoint_task_move_key;
ALTER TABLE actor_checkpoint ADD CONSTRAINT actor_checkpoint_task_action_key UNIQUE (task_id, action_id);
