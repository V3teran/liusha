-- 0034: 收紧 engagement.mode CHECK 约束为 mode='proxy'。
-- mode 字段保留（不删列），proxy 模式是当前唯一形态。

ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode = 'proxy');
