-- 0004 down: 反向回滚 finding global dedup
ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_host_nonempty;
DROP INDEX IF EXISTS finding_host_dedup_uniq;
CREATE UNIQUE INDEX finding_uniq ON finding (engagement_id, dedup_key);
ALTER TABLE finding DROP COLUMN IF EXISTS host;
