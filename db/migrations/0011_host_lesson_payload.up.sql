-- 0011: host_lesson 加 payload jsonb 列
--
-- 背景：lesson.content 是 LLM 提取的中文经验文本（≤500 字），人类可读但不利于
-- 程序化消费（聚类/统计/重放）。新增 payload jsonb 落结构化字段：
--   {
--     "method": "GET",
--     "url_template": "/api/bac/order/:id",
--     "payload_string": "?id=2",
--     "headers": {"X-User":"test"},
--     "notes": "...",
--     ...
--   }
-- 由 LessonExtract LLM 在生成 content 的同时输出 JSON payload。
--
-- 默认 '{}': 存量 3 行向后兼容（仍可读 content，只是 payload 空）。
ALTER TABLE host_lesson
    ADD COLUMN payload jsonb NOT NULL DEFAULT '{}'::jsonb;
