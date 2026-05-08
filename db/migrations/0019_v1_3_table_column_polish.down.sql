-- 0019 down: 回滚 v1.3 命名抛光

-- ===== 2. lesson → host_lesson =====
ALTER TABLE lesson RENAME CONSTRAINT lesson_source_finding_id_fkey TO host_lesson_source_finding_id_fkey;
ALTER TABLE lesson RENAME CONSTRAINT lesson_source_engagement_id_fkey TO host_lesson_source_engagement_id_fkey;
ALTER TABLE lesson RENAME CONSTRAINT lesson_priority_check TO host_lesson_priority_check;
ALTER TABLE lesson RENAME CONSTRAINT lesson_host_check TO host_lesson_host_check;
ALTER TABLE lesson RENAME CONSTRAINT lesson_content_check TO host_lesson_content_check;
ALTER INDEX lesson_lookup RENAME TO host_lesson_lookup;
ALTER INDEX lesson_dedup RENAME TO host_lesson_dedup;
ALTER INDEX lesson_pkey RENAME TO host_lesson_pkey;
ALTER TABLE lesson RENAME TO host_lesson;

-- ===== 1. engagement.target_host → scope_host =====
ALTER TABLE engagement RENAME COLUMN target_host TO scope_host;
