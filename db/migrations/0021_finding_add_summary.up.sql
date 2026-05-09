-- 0021: finding 加 summary 字段（自由文本主体）
--
-- 借鉴 Cairn Fact.description 的"自由文本优先"思路（不抄实现）：摆脱 evidence jsonb
-- 的 schema 束缚，summary 才是漏洞发现的核心载体——LLM 用自然语言描述发现是什么、
-- 怎么验证、置信度推理。evidence 退化为可选的结构化补充。
--
-- 历史 finding 默认空字符串，新写入由 write_finding 工具层强制非空。
ALTER TABLE finding ADD COLUMN summary text NOT NULL DEFAULT '';

COMMENT ON COLUMN finding.summary IS
  '漏洞自由文本描述（LLM 必填）：是什么、怎么验证、推理依据。'
  '替代旧的"主要靠 evidence jsonb 表达"模式，evidence 现退化为可选结构化补充。';
