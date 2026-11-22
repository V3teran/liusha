-- 检索模型可配置：服务地址与两个模型名不再写死为 Jina（用户可在「模型配置」页填写兼容端点）。
-- 空串 = 出厂默认（https://api.jina.ai / jina-embeddings-v5-text-small / jina-reranker-v2-base-multilingual），
-- 沿用「出厂默认 + 用户层」语义；向量维度仍绑定 corpus.embedding 的 vector(1024)，测试时实测校验。
ALTER TABLE public.retrieval_model
    ADD COLUMN base_url text NOT NULL DEFAULT '',
    ADD COLUMN embedding_model text NOT NULL DEFAULT '',
    ADD COLUMN rerank_model text NOT NULL DEFAULT '';

COMMENT ON COLUMN public.retrieval_model.base_url IS '检索服务地址（空 = 出厂默认 https://api.jina.ai）';
COMMENT ON COLUMN public.retrieval_model.embedding_model IS '向量模型名（空 = 出厂默认；维度须与 vector(1024) 一致，测试时实测校验）';
COMMENT ON COLUMN public.retrieval_model.rerank_model IS '重排模型名（空 = 出厂默认）';
