-- 0179 down: 回滚到 dedup_key v2 公式（host + cwe_id + path 前 40 字）。
-- 注意：v3 删除的重复行不可恢复（数据丢失单向）。

-- 1. 删除 v3 索引
DROP INDEX IF EXISTS finding_task_dedup_v3_uniq;

-- 2. 删除 v3 列
ALTER TABLE finding DROP COLUMN dedup_key;

-- 3. 恢复 v2 生成列（0049 公式）
ALTER TABLE finding ADD COLUMN dedup_key text GENERATED ALWAYS AS (
    lower(host)
    || '|' || lower(coalesce(cwe_id, ''))
    || '|' || lower(substring(coalesce(target->>'path', summary), 1, 40))
) STORED;

-- 4. v2 公式重新去重（保留 first_seen_at 最早的行）
DELETE FROM finding f
USING (
    SELECT id,
           row_number() OVER (
               PARTITION BY task_id, dedup_key
               ORDER BY first_seen_at, id
           ) AS rn
    FROM finding
) d
WHERE f.id = d.id AND d.rn > 1;

-- 5. 恢复 v2 UNIQUE 索引
CREATE UNIQUE INDEX finding_task_dedup_uniq ON finding (task_id, dedup_key);

COMMENT ON COLUMN finding.dedup_key IS 'v2 公式：host + cwe_id + (target.path 或 summary 前 40 字)';
