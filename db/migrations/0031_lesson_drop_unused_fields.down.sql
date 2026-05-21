-- 0031 down：还原 3 个 lesson 字段 + FK
--
-- 注意：down 后 source_engagement_id/source_finding_id 列恢复但所有旧行为 NULL
--（应用层从未写入过实际值），structured_payload 全部退化为 '{}'。
ALTER TABLE lesson ADD COLUMN source_engagement_id uuid REFERENCES engagement(id) ON DELETE SET NULL;
ALTER TABLE lesson ADD COLUMN source_finding_id    uuid REFERENCES finding(id)    ON DELETE SET NULL;
ALTER TABLE lesson ADD COLUMN structured_payload   jsonb NOT NULL DEFAULT '{}'::jsonb;
