-- 0030: 删除 tenant 多租户隔离设计
--
-- 背景：v1.1 单租户阶段，tenant_id 全部为 'default'，从未实际区分任何东西。
-- 多租户上线时再加（schema/索引/Go 代码一并加回）。
-- 用户要求"不保留全删干净，不兼容"——彻底清理 schema + Go 代码。

-- 1) engagement: 删 tenant_id 列 + 重建 active uniq 索引
DROP INDEX IF EXISTS engagement_active_uniq;
ALTER TABLE engagement DROP COLUMN tenant_id;
CREATE UNIQUE INDEX engagement_active_uniq ON engagement (target_host) WHERE status = 'active';

-- 2) lesson: 删 tenant_id 列 + 重建 dedup uniq + lookup 索引
DROP INDEX IF EXISTS lesson_dedup;
DROP INDEX IF EXISTS lesson_lookup;
ALTER TABLE lesson DROP COLUMN tenant_id;
CREATE UNIQUE INDEX lesson_dedup ON lesson (host, content_hash);
CREATE INDEX lesson_lookup ON lesson (host, kind, priority DESC, updated_at DESC);
