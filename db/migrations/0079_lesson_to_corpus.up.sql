-- 0079: lesson → corpus 跨目标知识库（hybrid RAG）。见 spec 2026-07-12-lesson-to-corpus-rag.md。
--
-- 砍 lesson 的 per-host 半（并入 lead），global 半升级为跨目标可检索知识库 corpus：
--   - 无 host 列：corpus 是跨目标的，host 归 lead（这是与 lesson 最本质的结构差异）。
--   - hybrid 检索：embedding(pgvector HNSW, dense) + content/title(pg_trgm, sparse) 两路召回。
--   - embedding 可空：写入先落行（content_hash 去重），embed 同步补；NULL 不进 dense 召回，仍可 sparse 命中。
--   - tags：场景/技术标签（sso:cas、waf:cloudflare...），检索前 metadata 硬过滤缩候选集。
--
-- 存量可丢（spec §9）：lesson 直接 DROP，纯 DDL 换表。

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE corpus (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 知识本体
    title          text NOT NULL DEFAULT '',              -- 一句话主题（人读 + 关键词命中）
    content        text NOT NULL CHECK (content <> ''),   -- 知识正文（打法/经验/规则）
    tags           text[] NOT NULL DEFAULT '{}',          -- 场景/技术标签，metadata 过滤用
    -- 来源
    source         text NOT NULL CHECK (source IN ('agent','expert')),  -- agent 自学蒸馏 / expert 外部导入
    source_task_id uuid,                                  -- agent 来源溯源到哪次 task（expert 为 NULL）
    -- 检索
    embedding      vector(1024),                          -- Jina jina-embeddings-v5-text-small；NULL=未 embed
    content_hash   text NOT NULL,                         -- SHA-256(content)，去重
    -- 运维
    hit_count      int NOT NULL DEFAULT 0,                -- 被检索命中并采纳次数（软优先级信号）
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- dense：HNSW 近似最近邻 + cosine（Jina normalized=true，向量已归一化）。
CREATE INDEX corpus_embedding_hnsw ON corpus USING hnsw (embedding vector_cosine_ops);

-- sparse：pg_trgm GIN（专有名词/代码标识符精确匹配优于分词全文检索）。
CREATE INDEX corpus_content_trgm ON corpus USING gin (content gin_trgm_ops);
CREATE INDEX corpus_title_trgm   ON corpus USING gin (title gin_trgm_ops);

-- tags metadata 过滤。
CREATE INDEX corpus_tags_gin ON corpus USING gin (tags);

-- 去重：同内容不重复写。
CREATE UNIQUE INDEX corpus_content_hash_uniq ON corpus (content_hash);

-- lesson 退役：per-host 半并入 lead，global 半升级为 corpus。存量可丢。
DROP TABLE IF EXISTS lesson;
