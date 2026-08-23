-- 0060 反向：恢复 http_flow 到 passive-only 形态。
-- 注意：active_scan 流量在 down 时会被 DELETE（passive 形态无法表达）。

-- 删新索引
DROP INDEX IF EXISTS http_flow_path_idx;
DROP INDEX IF EXISTS http_flow_source_idx;
DROP INDEX IF EXISTS http_flow_agent_idx;
DROP INDEX IF EXISTS http_flow_owner_host_idx;

-- 加回 passive_session_id 字段
ALTER TABLE http_flow ADD COLUMN passive_session_id uuid;

-- 回填：仅 owner_type=passive_session 行可恢复
UPDATE http_flow
   SET passive_session_id = owner_id
 WHERE owner_type = 'passive_session';

-- 删 active 数据（不能回退到 passive-only schema）
DELETE FROM http_flow WHERE owner_type = 'active_scan';

ALTER TABLE http_flow ALTER COLUMN passive_session_id SET NOT NULL;
ALTER TABLE http_flow ADD CONSTRAINT http_flow_passive_session_id_fkey
    FOREIGN KEY (passive_session_id) REFERENCES passive_session(id) ON DELETE CASCADE;

-- 恢复老索引
CREATE INDEX http_flow_host_idx ON http_flow (passive_session_id, host);
CREATE INDEX http_flow_passive_session_idx
    ON http_flow (passive_session_id) WHERE passive_session_id IS NOT NULL;

-- 删新字段
ALTER TABLE http_flow DROP CONSTRAINT http_flow_owner_type_check;
ALTER TABLE http_flow DROP COLUMN path;
ALTER TABLE http_flow DROP COLUMN duration_ms;
ALTER TABLE http_flow DROP COLUMN agent_id;
ALTER TABLE http_flow DROP COLUMN source;
ALTER TABLE http_flow DROP COLUMN owner_id;
ALTER TABLE http_flow DROP COLUMN owner_type;
