-- 0094 down: 恢复 scenario.domain 列（回退用）。
ALTER TABLE scenario ADD COLUMN domain text NOT NULL DEFAULT 'web';
