-- 0024 回滚：恢复 finding 三列 + 重建 severity CHECK + 重建 dedup 索引
-- 注意：原 kind/confidence/dedup_key 数据**不可恢复**（DROP COLUMN 已丢失）
ALTER TABLE finding ADD COLUMN kind text NOT NULL DEFAULT '';
ALTER TABLE finding ADD COLUMN confidence text NOT NULL DEFAULT 'medium';
ALTER TABLE finding ADD COLUMN dedup_key text NOT NULL DEFAULT '';

ALTER TABLE finding ADD CONSTRAINT finding_severity_check
  CHECK (severity = ANY (ARRAY['info'::text, 'low'::text, 'medium'::text, 'high'::text, 'critical'::text]));

CREATE INDEX finding_host_dedup_idx ON finding (host, dedup_key);
