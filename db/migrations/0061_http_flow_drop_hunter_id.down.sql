-- 0061 反向：加回 hunter_id 列（全 NULL，无法回填原值因为已丢失）

ALTER TABLE http_flow ADD COLUMN hunter_id uuid REFERENCES hunter(id) ON DELETE SET NULL;
CREATE INDEX http_flow_hunter_idx ON http_flow (hunter_id) WHERE hunter_id IS NOT NULL;
