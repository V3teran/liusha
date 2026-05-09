-- 0025: 删 finding_relation 表
--
-- 原意图：让 LLM 显式声明 finding A enables finding B 的组合漏洞关系。
-- agentic 化后：LLM 在 finding.summary 自由文本里直接描述
-- （"该 IDOR 依赖前面 f001 拿到的 admin cookie"），无需结构化关系表。
--
-- graph projector 也跟随简化：origin → finding → goal 三层，无 enables 边。

DROP TABLE IF EXISTS finding_relation;
