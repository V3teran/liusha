-- 0048 down: 删 UNIQUE 索引 + 删 dedup_key 生成列。
-- 注意：up 删的重复 finding 行不可恢复（数据丢失单向）。

DROP INDEX IF EXISTS finding_owner_dedup_uniq;
ALTER TABLE finding DROP COLUMN dedup_key;
