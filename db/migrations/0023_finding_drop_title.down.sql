-- 0023 回滚：重建 finding.title 列。
-- 注意：原 title 数据不可恢复（DROP COLUMN 已丢失），仅恢复 schema 形态。
ALTER TABLE finding ADD COLUMN title text NOT NULL DEFAULT '';
