-- 0004: finding 全局 dedup
--
-- 背景：v1.0 用 UNIQUE (engagement_id, dedup_key)，跨 engagement 同 endpoint
-- 重复扫描会创建多份 finding。proxy 模式 engagement 滚动后会反复入库相同漏洞，
-- 整站模式重复扫同站也会冗余。
--
-- v1.2 改为按 (host, dedup_key) 全局唯一：
--   - host 列从 target.host 提取并显式存储（便于 ExistsByHostKey 索引扫描）
--   - 同一 (host, dedup_key) 跨 engagement 共享一行；Save() ON CONFLICT 合并 evidence
--   - finding.engagement_id 仍保留，记录"首次发现 engagement"
--
-- 注：v1 单租户（tenant_id 固定 'default'），未来上多租户需改成 (tenant_id, host, dedup_key)。

-- 1. 加 host 列；存量数据从 target 提取
ALTER TABLE finding ADD COLUMN host text NOT NULL DEFAULT '';
UPDATE finding SET host = COALESCE(target->>'host', '');

-- 2. 切换 unique：先去旧索引，再建新索引
DROP INDEX IF EXISTS finding_uniq;
CREATE UNIQUE INDEX finding_host_dedup_uniq ON finding (host, dedup_key);

-- 3. host 不能为空（强约束，新写入必填）
ALTER TABLE finding ADD CONSTRAINT finding_host_nonempty CHECK (host <> '');
