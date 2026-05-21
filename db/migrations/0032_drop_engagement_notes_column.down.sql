-- 0032 down: 恢复 engagement.notes 列（仅为对称回滚；存量数据已永久销毁）。

ALTER TABLE engagement ADD COLUMN notes jsonb NOT NULL DEFAULT '{}'::jsonb;
