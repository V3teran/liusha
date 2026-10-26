-- 0179: finding.dedup_key 公式 v3 — 增加 method + parameter，防止假阳性（漏洞丢失）。
--
-- v2 公式（0049）问题：
--   dedup_key = host + cwe_id + path前40字
--
--   假阳性案例（高危）：
--     Finding A: POST /api/users?id=1 (读取用户，参数 id)
--     Finding B: POST /api/users?name=admin (创建管理员，参数 name)
--     → 两者 path 都是 /api/users，cwe 都是 CWE-89
--     → dedup_key 相同 → B 被去重 → 用户看不到第二个漏洞
--
-- v3 公式：
--   dedup_key = host + cwe_id + normalize_path(path) + method + parameter
--
--   改进点：
--   1. normalize_path：去掉 query string、去掉尾部 /、合并连续 /
--      - /api/users?id=1  → /api/users
--      - /api/users/      → /api/users
--      - /api//users///   → /api/users
--   2. 增加 method：区分 GET/POST/PUT/DELETE
--   3. 增加 parameter：注入参数名（LLM 需填写 target.parameter）
--
--   解决假阳性：
--     Finding A: POST /api/users?id=1, method=POST, parameter=id
--       → dedup_key = example.com|cwe-89|/api/users|post|id
--     Finding B: POST /api/users?name=admin, method=POST, parameter=name
--       → dedup_key = example.com|cwe-89|/api/users|post|name
--     → 两者 dedup_key 不同 → 都会被保留 ✅
--
-- 对齐业界：Burp Suite / OWASP ZAP 的去重都包含 method + parameter。

-- 1. 删除旧 UNIQUE 索引（CASCADE 会自动删除）
DROP INDEX IF EXISTS finding_task_dedup_uniq;

-- 2. 增加新 dedup_key_v3 生成列
ALTER TABLE finding ADD COLUMN dedup_key_v3 text GENERATED ALWAYS AS (
    lower(host)
    || '|' || lower(coalesce(cwe_id, ''))
    || '|' || lower(
        regexp_replace(
            rtrim(
                split_part(
                    coalesce(target->>'path', substring(summary, 1, 40)),
                    '?',  -- 去掉 query string
                    1
                ),
                '/'  -- 去掉尾部 /
            ),
            '/+',  -- 合并连续 /
            '/',
            'g'
        )
    )
    || '|' || lower(coalesce(target->>'method', ''))
    || '|' || lower(coalesce(target->>'parameter', ''))
) STORED;

-- 3. 新公式重新去重（保留 first_seen_at 最早的行）
DELETE FROM finding f
USING (
    SELECT id,
           row_number() OVER (
               PARTITION BY task_id, dedup_key_v3
               ORDER BY first_seen_at, id
           ) AS rn
    FROM finding
) d
WHERE f.id = d.id AND d.rn > 1;

-- 4. 创建新 UNIQUE 索引
CREATE UNIQUE INDEX finding_task_dedup_v3_uniq ON finding (task_id, dedup_key_v3);

-- 5. 删除旧列，重命名新列
ALTER TABLE finding DROP COLUMN dedup_key;
ALTER TABLE finding RENAME COLUMN dedup_key_v3 TO dedup_key;

COMMENT ON COLUMN finding.dedup_key IS 'v3 公式：host + cwe_id + normalize_path(path) + method + parameter（防止假阳性）';
