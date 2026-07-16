-- 0080: finding.source_flow_id → source_traffic_id（命名统一到 traffic）
--
-- 背景：流量工具/类型/prompt 已全统一 flow→traffic（见 refactor/passive-fullread-traffic-naming）。
-- finding 指向来源流量的列仍叫 source_flow_id，是命名链上最后一处 flow 残留，一并对齐。
--
-- 拆表后（0075）该列的外键（原指 http_flow）已随 http_flow 删除而消失，现为裸 bigint，
-- 一列可指 proxy_traffic / agent_traffic 任一（passive→proxy、active→agent），故不重建外键。
-- 纯 rename 列 + partial index，无数据变更、无约束处理。
ALTER TABLE finding RENAME COLUMN source_flow_id TO source_traffic_id;

ALTER INDEX finding_source_flow_idx RENAME TO finding_source_traffic_idx;
