-- 0062 反向：再次去掉 agent_id 列（与 0061 up 等价）

DROP INDEX IF EXISTS http_flow_agent_idx;
ALTER TABLE http_flow DROP COLUMN agent_id;
