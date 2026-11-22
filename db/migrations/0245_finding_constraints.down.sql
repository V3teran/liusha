-- 回到约束收紧之前（规整过的严重度不恢复；first_seen_at 以 created_at 回填）。
CREATE INDEX idx_finding_task_created ON finding (task_id, created_at DESC);
CREATE INDEX idx_finding_target_gin ON finding USING gin (target);
CREATE INDEX idx_finding_target_host ON finding ((target ->> 'host'));

ALTER TABLE finding DROP CONSTRAINT finding_class_check;
ALTER TABLE finding DROP CONSTRAINT finding_severity_check;

ALTER TABLE finding ADD COLUMN first_seen_at timestamptz NOT NULL DEFAULT now();
UPDATE finding SET first_seen_at = created_at;

ALTER TABLE finding DROP CONSTRAINT finding_result_node_uniq;
CREATE UNIQUE INDEX finding_task_result_uniq ON finding (task_id, result_node_id);
