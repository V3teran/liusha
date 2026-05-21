-- 0049: finding.dedup_key 公式 v2 — 加 CWE + path/endpoint 提取，从"宁可漏判"切到"宁可误判"。
--
-- v1 公式（0048）：lower(host) || '|' || lower(substring(summary, 1, 60))
--   问题：LLM 措辞不稳，"SQL Injection in /sqli/ — UNION-based, MariaDB" vs
--         "SQL Injection in /sqli/ — UNION-based data exfiltration (id"
--         前 60 字 lower 不同 → 算独立 finding，e2e 14 行实际有 ~4 对语义重复
--
-- v2 公式：lower(host) || '|' || lower(coalesce(cwe_id, '')) || '|' ||
--          lower(substring(coalesce(target->>'path', summary), 1, 40))
--   - 加 cwe_id：CWE 编号是稳定的语义锚（CWE-89 = SQLi）
--   - 优先 target->>'path'：LLM 在 jsonb target 里写 path 比 summary 措辞稳
--     fallback summary 是为了缺 path 时不丢 dedup
--   - 截到 40 字（vs 60）：更严格
--   - 用户语境：宁可误判（同 CWE+path 视为重复）不漏判
--
-- PG 限制：生成列表达式不能 ALTER，必须 DROP + ADD。
-- 旧 dedup_key 列上的 UNIQUE 索引会随列 DROP 自动消失。

-- 1. 删旧 UNIQUE + 旧生成列
DROP INDEX IF EXISTS finding_owner_dedup_uniq;
ALTER TABLE finding DROP COLUMN dedup_key;

-- 2. 加新 dedup_key v2 生成列
ALTER TABLE finding ADD COLUMN dedup_key text GENERATED ALWAYS AS (
    lower(host)
    || '|' || lower(coalesce(cwe_id, ''))
    || '|' || lower(substring(coalesce(target->>'path', summary), 1, 40))
) STORED;

-- 3. 新公式重新去重（保留 first_seen_at 最早行）
DELETE FROM finding f
USING (
    SELECT id,
           row_number() OVER (
               PARTITION BY owner_id, dedup_key
               ORDER BY first_seen_at, id
           ) AS rn
    FROM finding
) d
WHERE f.id = d.id AND d.rn > 1;

-- 4. 加新 UNIQUE 索引
CREATE UNIQUE INDEX finding_owner_dedup_uniq ON finding (owner_id, dedup_key);

COMMENT ON COLUMN finding.dedup_key IS 'v2 公式：host + cwe_id + (target.path 或 summary 前 40 字)';
