-- 0239 回退：恢复标签、计数与来源任务列（印证表折回来源任务，取最早的一条）。

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

ALTER TABLE corpus
    DROP CONSTRAINT corpus_title_check,
    DROP COLUMN terms,
    ADD COLUMN tags text[] DEFAULT '{}'::text[] NOT NULL,
    ADD COLUMN hit_count integer DEFAULT 0 NOT NULL,
    ADD COLUMN source_task_id uuid;

UPDATE corpus c SET source_task_id = f.task_id
FROM (
    SELECT DISTINCT ON (entry_id) entry_id, task_id
    FROM corpus_confirmation
    ORDER BY entry_id, confirmed_at
) f
WHERE f.entry_id = c.id;

DROP INDEX corpus_scenario_idx;
ALTER TABLE corpus DROP COLUMN scenario;
DROP TABLE corpus_confirmation;
DROP FUNCTION corpus_terms(text);

CREATE INDEX corpus_content_trgm ON corpus USING gin (content gin_trgm_ops);
CREATE INDEX corpus_title_trgm ON corpus USING gin (title gin_trgm_ops);
CREATE INDEX corpus_tags_gin ON corpus USING gin (tags);
