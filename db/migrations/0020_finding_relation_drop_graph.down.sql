-- 0020 down: 回滚 finding_relation + 复原 graph_node / graph_edge（schema 形态，
-- 不复原数据：graph_node/graph_edge 在 v1.3 收尾后已无消费者）。

DROP INDEX IF EXISTS finding_relation_to;
DROP INDEX IF EXISTS finding_relation_from;
DROP INDEX IF EXISTS finding_relation_uniq;
DROP TABLE IF EXISTS finding_relation;

CREATE TABLE graph_node (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    dedup_key     text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX graph_node_uniq ON graph_node (engagement_id, kind, dedup_key);

CREATE TABLE graph_edge (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid        NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    from_id       uuid        NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    to_id         uuid        NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    kind          text        NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX graph_edge_uniq ON graph_edge (engagement_id, from_id, to_id, kind);
