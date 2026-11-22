-- 检索模型密钥单行表：向量与重排共用一把密钥，前端「模型配置」页维护（原 embedding.api_key
-- 配置路径删除，密钥从此与对话 provider 密钥同机制——AES-256-GCM 加密落库）。
-- 无行 = 未配置（知识库只做字面检索，不阻塞对话与任务）。
CREATE TABLE public.retrieval_model (
    id boolean NOT NULL,
    encrypted_api_key bytea NOT NULL,
    api_key_last4 text NOT NULL DEFAULT '',
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT retrieval_model_pkey PRIMARY KEY (id),
    CONSTRAINT retrieval_model_single_row CHECK (id)
);

COMMENT ON TABLE public.retrieval_model IS '检索模型密钥（单行，无行 = 未配置；密文 AES-256-GCM，加密密钥是 llm.encryption_key）';
