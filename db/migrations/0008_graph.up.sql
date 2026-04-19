CREATE TABLE graph_node (
    id              uuid PRIMARY KEY,
    engagement_id   uuid NOT NULL REFERENCES engagement(id),
    kind            text NOT NULL
                    CHECK (kind IN ('surface','hypothesis','finding','exploit_chain','evidence')),
    payload         jsonb NOT NULL,
    vector          vector(1536),
    embedding_model text,
    dedup_key       text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_node (engagement_id, kind, created_at DESC);
CREATE INDEX ON graph_node USING hnsw (vector vector_cosine_ops)
    WHERE vector IS NOT NULL;
CREATE UNIQUE INDEX ON graph_node (engagement_id, dedup_key)
    WHERE dedup_key IS NOT NULL;

CREATE TABLE graph_edge (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    engagement_id uuid NOT NULL,
    src_id        uuid NOT NULL REFERENCES graph_node(id),
    dst_id        uuid NOT NULL REFERENCES graph_node(id),
    kind          text NOT NULL
                  CHECK (kind IN ('finding','chains_to','evidences','enables')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON graph_edge (engagement_id, src_id, kind);
CREATE INDEX ON graph_edge (engagement_id, dst_id, kind);
