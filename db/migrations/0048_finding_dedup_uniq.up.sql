-- 0048: finding 加 dedup_key 生成列 + UNIQUE(owner_id, dedup_key) 防父子并发重复写。
--
-- 设计动因：
--   - active 模式父子 agent 共用容器并发跑 ReAct loop
--   - 各自调 write_finding 时 read_findings 看到的快照可能错过对方刚写入的（race 6-60s）
--   - LLM 层 dedup 不可靠 → DB 层 UNIQUE 兜底
--
-- dedup_key 公式：lower(host) || '|' || lower(substring(summary, 1, 60))
--   - 偏漏判（措辞不稳易判为不同）而非误判（不同漏洞误合并）—— 安全方向
--   - lesson 表已有同模式 UNIQUE(host, content_hash)，本次给 finding 补齐

-- 1. 加生成列（PG 自动 backfill 所有现存行）
ALTER TABLE finding ADD COLUMN dedup_key text GENERATED ALWAYS AS (
    lower(host) || '|' || lower(substring(coalesce(summary, ''), 1, 60))
) STORED;

-- 2. 现存重复清理：按 (owner_id, dedup_key) 保留 first_seen_at 最早的行
--    必须先清重才能加 UNIQUE 约束
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

-- 3. 加 UNIQUE 索引
CREATE UNIQUE INDEX finding_owner_dedup_uniq ON finding (owner_id, dedup_key);

COMMENT ON COLUMN finding.dedup_key IS 'host+summary 前 60 字（lower）拼接，Save 用 ON CONFLICT 兜底';
