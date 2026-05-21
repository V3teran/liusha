-- 0049 down: 还原 dedup_key v1 公式（lower(host) + summary 前 60 字）。
-- 注意：v2 删的额外重复行不可恢复（数据丢失单向）。

DROP INDEX IF EXISTS finding_owner_dedup_uniq;
ALTER TABLE finding DROP COLUMN dedup_key;

ALTER TABLE finding ADD COLUMN dedup_key text GENERATED ALWAYS AS (
    lower(host) || '|' || lower(substring(coalesce(summary, ''), 1, 60))
) STORED;

CREATE UNIQUE INDEX finding_owner_dedup_uniq ON finding (owner_id, dedup_key);
