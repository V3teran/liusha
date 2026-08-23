-- 0061 反向：加回 agent_id 列（全 NULL，无法回填原值因为已丢失）

ALTER TABLE http_flow ADD COLUMN agent_id uuid REFERENCES agent(id) ON DELETE SET NULL;
CREATE INDEX http_flow_agent_idx ON http_flow (agent_id) WHERE agent_id IS NOT NULL;
