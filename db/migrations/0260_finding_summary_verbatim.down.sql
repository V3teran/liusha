-- 回滚：恢复摘要的长度与换行约束。存量里有超长或带换行的摘要时约束加不回去，如实失败。
ALTER TABLE finding ADD CONSTRAINT finding_summary_max_500 CHECK ((length(summary) <= 500));
ALTER TABLE finding ADD CONSTRAINT finding_summary_one_line CHECK ((summary !~ '\n'::text));

COMMENT ON COLUMN public.finding.summary IS '漏洞描述（必填）：是什么、怎么验证、推理依据；evidence 为可选的结构化补充';
