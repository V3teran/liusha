-- endpoint 加 name 字段：界面上的功能名字（如 "Reflected XSS"），区别于 path（如 /vulnerabilities/xss_r/）。
--
-- 设计动机：sitemap 视图需要"人能读"的功能标签，而不是只显示 URL path。commander recon 浏览
-- 时能从 nav/title/breadcrumb 提取出业务模块名，write_endpoint 时一并传入。
--
-- 可空：API 类 endpoint（如 /api/v1/orders）可能没有界面对应名字，name 为 NULL 时前端 fallback
-- 显示 method + path。

ALTER TABLE endpoint ADD COLUMN name text;
