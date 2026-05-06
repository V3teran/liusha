-- 0016: confidence enum 从"占位 unverified/verified/rejected"改为"LLM 自评 high/medium/low"
--
-- agentic 路线：v1 占位语义（unverified/verified/rejected 仅给未来人工/自动验证流程用）已下线；
-- 当前 LLM 写 finding 时按 verification_path / 信号强度自评 high / medium / low。
--
-- 注：旧值历史数据迁移——这是 dev 阶段，无生产数据，直接 drop+add；若有存量数据需先映射
-- （unverified → medium / verified → high / rejected → low）。

ALTER TABLE vuln_finding DROP CONSTRAINT IF EXISTS finding_confidence_check;
ALTER TABLE vuln_finding ADD CONSTRAINT finding_confidence_check
    CHECK (confidence = ANY (ARRAY['high'::text, 'medium'::text, 'low'::text]));
