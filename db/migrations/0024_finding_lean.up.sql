-- 0024: finding 表瘦身（彻底 agentic 化）
--
-- 删除三列：
--   - kind        (软标签，信息已融入 summary 自由文本；UI 配色改用 severity)
--   - confidence  (enum 三档，信息已融入 summary 推理段)
--   - dedup_key   (NormalizeDedupKey 强制结构化失去意义；dedup 由 LLM 自决——
--                  写 finding 前先 findings() 看 host 已有的，自己判要不要再写)
--
-- 软化 severity：删 CHECK 约束让 LLM 自由命名（critical/high/medium/low/info/P1/任意）。
-- 前端配色按前缀匹配（critical→红、high→橙、medium→黄、low→灰、其他→蓝）。
--
-- 借鉴 Cairn Fact 极简 schema 的思路（不抄实现）：发现物只剩自由文本 summary +
-- 软分类 severity + 可选 evidence jsonb。

ALTER TABLE finding DROP COLUMN kind;
ALTER TABLE finding DROP COLUMN confidence;
ALTER TABLE finding DROP COLUMN dedup_key;

-- 删 severity enum CHECK 约束
ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_severity_check;

COMMENT ON COLUMN finding.severity IS
  'LLM 自由文本（建议 critical/high/medium/low/info 保持配色一致；其他值 UI 退化为蓝色）';

-- 删旧的 (host, dedup_key) 索引（dedup_key 列已不存在，索引自动会被 PG 拒绝；显式 DROP 让 migration 幂等）
DROP INDEX IF EXISTS finding_host_dedup_idx;
