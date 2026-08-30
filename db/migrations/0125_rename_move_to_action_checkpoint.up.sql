-- 0125: 重命名 actor_checkpoint 表的列：move_id → action_id
-- 这是 Move → Action 重构的一部分

-- 1. 删除旧索引
DROP INDEX IF EXISTS actor_checkpoint_scan_move_idx;

-- 2. 删除旧唯一约束
ALTER TABLE actor_checkpoint DROP CONSTRAINT IF EXISTS actor_checkpoint_scan_id_move_id_key;

-- 3. 重命名列
ALTER TABLE actor_checkpoint RENAME COLUMN move_id TO action_id;

-- 4. 创建新索引
CREATE INDEX actor_checkpoint_scan_action_idx ON actor_checkpoint (scan_id, action_id);

-- 5. 添加新唯一约束
ALTER TABLE actor_checkpoint ADD CONSTRAINT actor_checkpoint_scan_id_action_id_key UNIQUE (scan_id, action_id);
