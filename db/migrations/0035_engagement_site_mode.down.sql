-- 回滚 0035：恢复 mode CHECK 仅接受 'proxy'。
-- 注意：回滚前必须确保不存在 mode='site' 的行，否则约束验证失败。

ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode = 'proxy');
