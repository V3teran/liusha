-- 0009 down: 撤销 source_flow_id 列。
DROP INDEX IF EXISTS finding_source_flow_idx;
ALTER TABLE finding DROP COLUMN IF EXISTS source_flow_id;
