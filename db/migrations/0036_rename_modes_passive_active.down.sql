-- 回滚 0036：把 passive/active 改回 proxy/site。

-- 1) 先 DROP 旧 CHECK
ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;

-- 2) 回滚 mode 值
UPDATE engagement SET mode='proxy' WHERE mode='passive';
UPDATE engagement SET mode='site'  WHERE mode='active';

-- 3) ADD 旧 CHECK
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode IN ('proxy', 'site'));

-- 3) 回滚唯一索引
DROP INDEX IF EXISTS engagement_active_passive_uniq;
CREATE UNIQUE INDEX engagement_active_proxy_uniq
  ON engagement ((1)) WHERE status = 'active' AND mode = 'proxy';
