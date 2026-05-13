-- 0030 down: 恢复 tenant_id 列 + 旧索引
DROP INDEX IF EXISTS engagement_active_uniq;
ALTER TABLE engagement ADD COLUMN tenant_id text NOT NULL DEFAULT 'default';
CREATE UNIQUE INDEX engagement_active_uniq ON engagement (tenant_id, target_host) WHERE status = 'active';

DROP INDEX IF EXISTS lesson_dedup;
DROP INDEX IF EXISTS lesson_lookup;
ALTER TABLE lesson ADD COLUMN tenant_id text NOT NULL DEFAULT 'default';
CREATE UNIQUE INDEX lesson_dedup ON lesson (tenant_id, host, content_hash);
CREATE INDEX lesson_lookup ON lesson (tenant_id, host, kind, priority DESC, updated_at DESC);
