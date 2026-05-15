-- 回滚 0033：恢复 target_host 单字段 + per-host 唯一索引

DROP INDEX IF EXISTS engagement_expires_idx;
DROP INDEX IF EXISTS engagement_active_proxy_uniq;

ALTER TABLE engagement DROP COLUMN expires_at;
ALTER TABLE engagement DROP COLUMN scope;

ALTER TABLE engagement ADD COLUMN target_host text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX engagement_active_uniq ON engagement (target_host) WHERE status = 'active';
