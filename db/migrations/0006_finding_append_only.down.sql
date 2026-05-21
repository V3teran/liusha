-- 0006 down: 回滚到 (host, dedup_key) UNIQUE
--
-- 注意：若已有 append-only 写入产生重复行，回滚会因 UNIQUE 约束冲突失败。
-- 需先手工去重才能 down：
--   DELETE FROM finding a USING finding b
--   WHERE a.id < b.id AND a.host = b.host AND a.dedup_key = b.dedup_key;

DROP INDEX IF EXISTS finding_host_dedup_idx;
CREATE UNIQUE INDEX finding_host_dedup_uniq ON finding (host, dedup_key);
