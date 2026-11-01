ALTER TABLE assignment DROP CONSTRAINT assignment_source_check;
UPDATE assignment SET source = 'manual' WHERE source = 'self';
UPDATE assignment SET source = 'auto' WHERE source = 'proxy';
ALTER TABLE assignment ADD CONSTRAINT assignment_source_check
    CHECK (source IN ('manual','auto'));
