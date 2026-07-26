-- TTFT（首 token 延迟）+ 流式标识：LLM 可观测性的核心两项。
--
-- ttft_ms 区分「模型思考慢」与「输出长导致总时长长」——只有 latency_ms 时两者无法区分。
-- 仅流式调用可测（非流式一次性返回，首 token 即末 token）；0 = 未测得/非流式。
-- is_stream 决定 ttft_ms 是否有意义，也决定前端如何解读延迟（对齐业界日志页做法）。
-- 历史行无法回填（原始时序已丢），留默认值 0/false。
ALTER TABLE llm_invocation
    ADD COLUMN ttft_ms   integer NOT NULL DEFAULT 0,
    ADD COLUMN is_stream boolean NOT NULL DEFAULT false;
