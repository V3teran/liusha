-- 0031 lesson 表瘦身：删 3 个从未生效的字段
--
-- 背景：
--   - source_engagement_id / source_finding_id：model 注释写"追溯用"但 lesson.Store.Add
--     从未写入实际值（caller 全部传 nil）。实测 5/5 行 NULL，纯 dead column。
--   - structured_payload：WriteLesson 工具暴露 payload 参数让 LLM 自填，但实测
--     5/5 行 = '{}'。LLM 用 content 自由文本沉淀经验完全够用，结构化字段是设计冗余。
--
-- DROP COLUMN 自动级联 FK constraint，不需要显式 DROP CONSTRAINT。
ALTER TABLE lesson DROP COLUMN source_engagement_id;
ALTER TABLE lesson DROP COLUMN source_finding_id;
ALTER TABLE lesson DROP COLUMN structured_payload;
