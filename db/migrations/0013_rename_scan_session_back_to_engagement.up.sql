-- 0013: scan_session 表名改回 engagement
--
-- 业界对照：渗透测试领域（DefectDojo / Cobalt / Faraday / Dradis）首选 engagement 行话；
-- v0012 改成 scan_session 与 engagement_id 列名出现"表名 / 列名"语义错位（FK 列从未跟着改）。
-- 现在改回 engagement，整张表与所有引用方的 *_id 列名重新对齐。
--
-- PG ALTER TABLE RENAME 自动更新所有 FK constraint 引用。
-- 索引 engagement_active_uniq 名字本来就一直没动，改回后表名/索引名也对齐。
ALTER TABLE scan_session RENAME TO engagement;
