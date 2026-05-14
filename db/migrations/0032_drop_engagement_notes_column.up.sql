-- 0032: 删除 engagement.notes 列
--
-- 背景：短期工作笔记板（hunter 跨 task 共享）从 PG jsonb 迁出至 Redis
-- （internal/notes 包，key=liusha:note:{engagement_id}，TTL 24h）。
-- PG 列已无任何 Go 代码写入 / 读取——store.AppendNote/ReadNotes 已删除。
--
-- 数据不可逆转：存量 jsonb 数据将丢失。这是预期销毁——notes 是 24h TTL
-- 短期记忆，运维不应将其视为长期资产。

ALTER TABLE engagement DROP COLUMN notes;
