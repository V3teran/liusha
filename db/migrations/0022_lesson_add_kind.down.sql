-- 0022 回滚：恢复 lesson 表无 kind 维度的状态。
DROP INDEX IF EXISTS lesson_lookup;
CREATE INDEX lesson_lookup ON lesson (tenant_id, host, priority DESC, updated_at DESC);

ALTER TABLE lesson DROP COLUMN kind;
