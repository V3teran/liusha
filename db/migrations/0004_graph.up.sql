-- 0004: Attack Graph —— graph_node + graph_edge 是平台的核心数据模型，
-- 所有渗透过程的发现、假设、证据、利用链都以 DAG 节点和关系存储，
-- 保证跨 agent/跨窗口的溯源与复用。
--
-- graph_node.vector 使用 pgvector 1536 维（对齐 OpenAI text-embedding-3-small），
-- 通过 HNSW + cosine 算子快速做语义相似检索与去重。
-- dedup_key 上的部分唯一索引避免同一 engagement 内重复落盘相同事实。
CREATE TABLE graph_node (
    id              uuid PRIMARY KEY,
    engagement_id   uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    kind            text NOT NULL
                    CHECK (kind IN ('surface','hypothesis','finding','exploit_chain','evidence')),
    payload         jsonb NOT NULL,
    vector          vector(1536),
    embedding_model text,
    dedup_key       text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- 按 engagement + 节点种类倒序列表（图谱浏览 / 时间线）。
CREATE INDEX graph_node_eng_kind_created_idx
    ON graph_node (engagement_id, kind, created_at DESC);
-- pgvector HNSW 索引：cosine 距离做语义近邻检索；构建慢但查询快。
CREATE INDEX graph_node_vector_hnsw_idx
    ON graph_node USING hnsw (vector vector_cosine_ops);
-- 同一 engagement 内 dedup_key 唯一，NULL 不参与去重（允许无法指纹化的节点并存）。
CREATE UNIQUE INDEX graph_node_dedup_idx
    ON graph_node (engagement_id, dedup_key)
    WHERE dedup_key IS NOT NULL;

-- graph_edge 建模节点间语义关系：
-- finding     —— 某 surface/hypothesis 得到 finding；
-- chains_to   —— 多个 finding 串成 exploit_chain；
-- evidences   —— evidence 为某 finding/chain 提供证据；
-- enables     —— 一个节点使能另一个节点（横向/纵向推进）。
CREATE TABLE graph_edge (
    id            uuid PRIMARY KEY,
    engagement_id uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    src_id        uuid NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    dst_id        uuid NOT NULL REFERENCES graph_node(id) ON DELETE CASCADE,
    kind          text NOT NULL
                  CHECK (kind IN ('finding','chains_to','evidences','enables')),
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- 按出边检索（从某节点展开下游图谱）。
CREATE INDEX graph_edge_src_idx ON graph_edge (engagement_id, src_id, kind);
-- 按入边检索（反查某节点的来源 / 证据）。
CREATE INDEX graph_edge_dst_idx ON graph_edge (engagement_id, dst_id, kind);
