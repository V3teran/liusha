-- 回滚 0065：删 http_flow 的 identity + tool 字段。
ALTER TABLE http_flow DROP COLUMN IF EXISTS tool;
ALTER TABLE http_flow DROP COLUMN IF EXISTS identity;
