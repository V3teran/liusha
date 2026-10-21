-- 扩展思考预算（anthropic thinking.budget_tokens）：>0 = 开启扩展思考
-- （模型先流式产出内部 CoT 再产正文），0 = 不开启。仅 anthropic 协议消费。
ALTER TABLE llm_provider ADD COLUMN thinking_budget_tokens bigint NOT NULL DEFAULT 0;
