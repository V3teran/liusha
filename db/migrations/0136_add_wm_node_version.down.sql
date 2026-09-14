-- Migration 0136: 回滚乐观锁版本号
--
-- 回滚步骤：
-- 1. 删除触发器
-- 2. 删除触发器函数
-- 3. 删除索引
-- 4. 删除 version 列

-- 1. 删除触发器
DROP TRIGGER IF EXISTS trigger_increment_wm_node_version ON wm_node;

-- 2. 删除触发器函数
DROP FUNCTION IF EXISTS increment_wm_node_version();

-- 3. 删除索引
DROP INDEX IF EXISTS idx_wm_node_id_version;

-- 4. 删除 version 列
ALTER TABLE wm_node DROP COLUMN IF EXISTS version;
