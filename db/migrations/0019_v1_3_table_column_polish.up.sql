-- 0019: v1.3 命名抛光 — 列名/表名最后一波整改
--
-- 改动一览：
--   1. engagement.scope_host → target_host
--      "scope" 在渗透测试圈语义模糊；target 是 OWASP/Burp/Nessus 通用术语
--      关联：唯一索引 engagement_active_uniq、Go 字段 ScopeHost
--   2. host_lesson 表 → lesson
--      包名已是 internal/lesson；表名带 host_ 前缀冗余（host 是其中一列，不是表前缀）
--      关联：所有 host_lesson_* 索引/约束 rename

-- ===== 1. engagement.scope_host → target_host =====
ALTER TABLE engagement RENAME COLUMN scope_host TO target_host;

-- ===== 2. host_lesson → lesson =====
ALTER TABLE host_lesson RENAME TO lesson;
ALTER INDEX host_lesson_pkey RENAME TO lesson_pkey;
ALTER INDEX host_lesson_dedup RENAME TO lesson_dedup;
ALTER INDEX host_lesson_lookup RENAME TO lesson_lookup;
ALTER TABLE lesson RENAME CONSTRAINT host_lesson_content_check TO lesson_content_check;
ALTER TABLE lesson RENAME CONSTRAINT host_lesson_host_check TO lesson_host_check;
ALTER TABLE lesson RENAME CONSTRAINT host_lesson_priority_check TO lesson_priority_check;
ALTER TABLE lesson RENAME CONSTRAINT host_lesson_source_engagement_id_fkey TO lesson_source_engagement_id_fkey;
ALTER TABLE lesson RENAME CONSTRAINT host_lesson_source_finding_id_fkey TO lesson_source_finding_id_fkey;
