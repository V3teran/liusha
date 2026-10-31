-- 拆除 finding.depends_on（0059 加入的组合漏洞依赖链）。
--
-- 依据（以源码为准）：全仓无任何生产代码给该列赋值——finding 的唯一写入方
-- 是 evaluator 晋升门经 ClaimSink 落库，载荷（ConfirmedClaim）不携带依赖关系；
-- 前端「关联漏洞」区块渲染的是恒空数组。注释声称的 sitemap projector 不存在。
-- 组合漏洞的依赖语义由 LLM 写进 summary/evidence 自由文本，无结构化生产者。
ALTER TABLE finding DROP COLUMN IF EXISTS depends_on;
