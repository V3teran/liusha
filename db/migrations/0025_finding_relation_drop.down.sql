-- 0025 回滚：重建 finding_relation（参考 0020.up.sql；数据已丢失）
CREATE TABLE finding_relation (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    from_finding_id  uuid        NOT NULL REFERENCES finding(id) ON DELETE CASCADE,
    to_finding_id    uuid        NOT NULL REFERENCES finding(id) ON DELETE CASCADE,
    kind             text        NOT NULL,
    payload          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT finding_relation_kind_check CHECK (kind IN ('enables')),
    CONSTRAINT finding_relation_no_self CHECK (from_finding_id <> to_finding_id)
);

CREATE UNIQUE INDEX finding_relation_uniq
    ON finding_relation (from_finding_id, to_finding_id, kind);
CREATE INDEX finding_relation_from ON finding_relation (from_finding_id);
CREATE INDEX finding_relation_to   ON finding_relation (to_finding_id);
