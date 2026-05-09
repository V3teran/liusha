-- 0021 回滚：删掉 finding.summary 列。
-- 注意：历史数据若已写入 summary 文本将丢失（DROP COLUMN 不可逆）。
ALTER TABLE finding DROP COLUMN summary;
