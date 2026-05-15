-- 回滚 0034：恢复 mode CHECK 接受 browser 选项

ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode IN ('proxy','browser'));
