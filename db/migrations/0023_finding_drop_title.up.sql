-- 0023: finding 删 title 列（与 summary 重复）
--
-- v0021 加 summary 自由文本主体后，title 与 summary 第一句严重重复——LLM 几乎总是
-- 把 "/api/foo 的 X 参数存在 SQL 注入" 同时塞 title 和 summary 开头。删 title 减少冗余。
--
-- UI/Label 场景统一改成取 summary 第一行（参考 git commit message convention：
-- 第一行 ≤ 72 chars 充当 subject，空行后是 body）。
--
-- 历史 finding 的 title 数据**不可恢复**（DOWN 仅重建空字段）。
ALTER TABLE finding DROP COLUMN title;
