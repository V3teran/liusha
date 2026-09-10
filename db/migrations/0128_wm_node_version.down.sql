-- 回滚：删除 version 字段

DROP INDEX IF EXISTS idx_wm_node_version;
ALTER TABLE wm_node DROP COLUMN IF EXISTS version;
