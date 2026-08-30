-- 0125: 回滚 action_id → move_id

-- 1. 删除新索引
DROP INDEX IF EXISTS actor_checkpoint_scan_action_idx;

-- 2. 删除新唯一约束
ALTER TABLE actor_checkpoint DROP CONSTRAINT IF EXISTS actor_checkpoint_scan_id_action_id_key;

-- 3. 重命名列回去
ALTER TABLE actor_checkpoint RENAME COLUMN action_id TO move_id;

-- 4. 恢复旧索引
CREATE INDEX actor_checkpoint_scan_move_idx ON actor_checkpoint (scan_id, move_id);

-- 5. 恢复旧唯一约束
ALTER TABLE actor_checkpoint ADD CONSTRAINT actor_checkpoint_scan_id_move_id_key UNIQUE (scan_id, move_id);
