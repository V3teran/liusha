-- 发现台账的摘要不再约束长度与换行。
--
-- summary 是坐实主张的陈述原文（交付时原样写入），而主张全文只受模型输出长度的约束。
-- 此前「不超过 500 字」的约束让超长的主张永远交付不进台账：实测 18 条坐实结果里丢了
-- 1 条 562 字的 SQL 注入，补交付每一轮都失败。「不含换行」同理是一条能拒绝合法主张的
-- 约束。坐实了的主张必须能进台账——台账任何一列的取值域都要存得下主张的合法取值。
-- 列表里按单行截断显示，全文在详情里。
ALTER TABLE finding DROP CONSTRAINT finding_summary_max_500;
ALTER TABLE finding DROP CONSTRAINT finding_summary_one_line;

COMMENT ON COLUMN public.finding.summary IS '坐实主张的陈述原文（评估理由在 evaluation 的 judge_reasoning）：是什么、怎么验证。不限长度与换行，列表里按单行截断显示';
