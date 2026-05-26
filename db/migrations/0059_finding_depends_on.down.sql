-- 回滚：重建 finding_relation 表 + 删 finding.depends_on
CREATE TABLE IF NOT EXISTS finding_relation (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    from_finding_id uuid NOT NULL,
    to_finding_id   uuid NOT NULL,
    kind            text NOT NULL CHECK (kind = 'enables'),
    payload         jsonb DEFAULT '{}'::jsonb,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (from_finding_id, to_finding_id, kind),
    CONSTRAINT finding_relation_no_self CHECK (from_finding_id <> to_finding_id)
);

ALTER TABLE finding DROP COLUMN IF EXISTS depends_on;
