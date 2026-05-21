-- 0027: 删 flow_facts 表
--
-- 该表是 v1.3 时代 classify_traffic 工具写入的"流量事实"jsonb；
-- v0024 删除 classify_traffic + flowfacts 包后，表 0 caller 0 数据，纯遗留 schema。

DROP TABLE IF EXISTS flow_facts;
