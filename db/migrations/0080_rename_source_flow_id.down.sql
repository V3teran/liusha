-- 0080 down: source_traffic_id → source_flow_id 回滚（纯 rename，与 up 对称）。
ALTER INDEX finding_source_traffic_idx RENAME TO finding_source_flow_idx;

ALTER TABLE finding RENAME COLUMN source_traffic_id TO source_flow_id;
