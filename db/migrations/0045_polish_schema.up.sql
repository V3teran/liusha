-- 0045: 命名细节 + 状态简化 + finding 元数据字段 + http_flow.host 提取。
--
-- 设计动因（4 项独立但低相关变更，合并一个 migration 减少 schema_migrations 噪声）：
--   1. 删 status 'archived' 取值——代码里从未实际写入此值（StatusArchived 是死常量）
--   2. active_scan.target_host SET NOT NULL——active 任务必有目标，零值用 '' 表达
--   3. http_flow 加 host text 列——避免按 url LIKE 'http://X/%' 查找的低效
--   4. finding 加 4 元数据字段 —— cwe_id / owasp_category / first_seen_at / remediation
--      用于报告生成、跨扫描去重、漏洞分类标准化

-- 1. 状态机简化：删 'archived' 值
ALTER TABLE passive_session DROP CONSTRAINT passive_session_status_check;
ALTER TABLE passive_session ADD CONSTRAINT passive_session_status_check
    CHECK (status IN ('active','aborted'));
ALTER TABLE active_scan DROP CONSTRAINT active_scan_status_check;
ALTER TABLE active_scan ADD CONSTRAINT active_scan_status_check
    CHECK (status IN ('active','aborted'));

-- 2. active_scan.target_host 必填（先把 NULL 折成 '' 再加约束）
UPDATE active_scan SET target_host = '' WHERE target_host IS NULL;
ALTER TABLE active_scan ALTER COLUMN target_host SET NOT NULL;
ALTER TABLE active_scan ALTER COLUMN target_host SET DEFAULT '';

-- 3. http_flow 加 host 列（按 host 过滤代替按 url LIKE）
ALTER TABLE http_flow ADD COLUMN host text NOT NULL DEFAULT '';
UPDATE http_flow SET host = COALESCE(
    substring(url FROM '^(?:https?://)?([^/:?]+)'),
    ''
) WHERE host = '';
CREATE INDEX http_flow_host_idx ON http_flow (passive_session_id, host);

-- 4. finding 元数据字段（CWE / OWASP / first_seen / remediation）
ALTER TABLE finding ADD COLUMN cwe_id text;
ALTER TABLE finding ADD COLUMN owasp_category text;
ALTER TABLE finding ADD COLUMN first_seen_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE finding ADD COLUMN remediation text;
COMMENT ON COLUMN finding.cwe_id IS '示例值: CWE-89 (SQL Injection)';
COMMENT ON COLUMN finding.owasp_category IS '示例值: A03:2021 (Injection)';
COMMENT ON COLUMN finding.first_seen_at IS '首次发现时间——Update 不变；created_at 保持一致';
COMMENT ON COLUMN finding.remediation IS '修复建议（自然语言）；evidence 仍存 PoC';
