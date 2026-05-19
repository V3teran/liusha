-- 回滚 0038（incremental 双轨）：DROP 新加 owner 列 + DROP 新表。engagement 表本就未动。
--
-- 数据影响：新表 passive_session / active_scan 行全丢；agent_run/finding/llm_invocation
-- 旧行不受影响（owner_type/owner_id NULL）。若 commit B 已写入新行（owner_type 非空），
-- 这部分行的 owner_id 信息丢失但行本身仍保留（engagement_id 双轨列还在）。

-- 1. http_flow 反向
DROP INDEX IF EXISTS http_flow_passive_session_idx;
ALTER TABLE http_flow DROP COLUMN passive_session_id;

-- 2. 共享表反向：DROP owner 列
DROP INDEX IF EXISTS agent_run_owner_idx;
ALTER TABLE agent_run DROP COLUMN owner_id;
ALTER TABLE agent_run DROP COLUMN owner_type;

DROP INDEX IF EXISTS finding_owner_idx;
ALTER TABLE finding DROP COLUMN owner_id;
ALTER TABLE finding DROP COLUMN owner_type;

DROP INDEX IF EXISTS llm_invocation_owner_idx;
ALTER TABLE llm_invocation DROP COLUMN owner_id;
ALTER TABLE llm_invocation DROP COLUMN owner_type;

-- 3. DROP 新增 2 表
DROP INDEX IF EXISTS passive_session_active_host_uniq;
DROP INDEX IF EXISTS passive_session_status_idx;
DROP TABLE passive_session;

DROP INDEX IF EXISTS active_scan_status_idx;
DROP TABLE active_scan;
