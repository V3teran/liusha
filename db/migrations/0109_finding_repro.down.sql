-- 0109 down: 移除 finding.repro 复现配方列。
ALTER TABLE finding DROP COLUMN IF EXISTS repro;
