-- 0062 反向：再次去掉 hunter_id 列（与 0061 up 等价）

DROP INDEX IF EXISTS http_flow_hunter_idx;
ALTER TABLE http_flow DROP COLUMN hunter_id;
