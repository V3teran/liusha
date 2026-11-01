-- 下发来源命名收口：manual/auto → self/proxy（自主下发 / 代理模式）。
-- conversation 无独立 source 列（经 assignment JOIN 派生），只需迁移
-- assignment 一张表：数据 UPDATE + CHECK 约束同步扩改。
ALTER TABLE assignment DROP CONSTRAINT assignment_source_check;
UPDATE assignment SET source = 'self' WHERE source = 'manual';
UPDATE assignment SET source = 'proxy' WHERE source = 'auto';
ALTER TABLE assignment ADD CONSTRAINT assignment_source_check
    CHECK (source IN ('self','proxy'));
