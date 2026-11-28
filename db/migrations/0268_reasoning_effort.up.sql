-- 扩展思考预算（thinking.budget_tokens）换成思考深度（reasoning_effort）：前者按 token 给思考
-- 分配预算，在最新的模型上已被弃用或直接拒绝（400），别家也从来没有过；各家都已转向「给一个
-- 档位，由模型自己决定想多少」。取值的唯一出处在 framework/llm（ReasoningEfforts）。
--
-- 旧预算换算不成档位，存量有预算的行落为「没设置」（按模型自己的默认）。
ALTER TABLE llm_provider DROP COLUMN thinking_budget_tokens;
ALTER TABLE llm_provider ADD COLUMN reasoning_effort text NOT NULL DEFAULT '';
ALTER TABLE llm_provider
    ADD CONSTRAINT ck_llm_provider_reasoning_effort
    CHECK (reasoning_effort IN ('', 'low', 'medium', 'high', 'max'));
