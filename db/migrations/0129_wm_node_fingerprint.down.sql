-- 回滚：删除 fingerprint 字段和索引

DROP INDEX IF EXISTS idx_wm_node_fingerprint_pending;
DROP INDEX IF EXISTS idx_wm_node_fingerprint;
ALTER TABLE wm_node DROP COLUMN IF EXISTS fingerprint;
