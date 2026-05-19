-- 0041: 破坏式清理——DROP engagement 表 + DROP engagement_id 列。
--
-- 前置条件：
--   - 0040 已 apply（DROP FK 完成）
--   - cmd/api / cmd/scanner 代码已不再读写 engagement 表 + engagement_id 列（B6.7/B6.8 完成）
--   - 数据回填策略：用户授权清数据，旧 engagement 行 + 4 张表的 engagement_id 列值直接丢
--   - integration 测试（internal/{agentrun,flow,llminvocation}/store_integration_test.go）仍
--     import internal/engagement 包；apply 本 migration 后这些测试会失败——需先重写或删除
--
-- 本 migration 破坏式：
--   1. DROP engagement_id 列（4 张共享表）+ 关联索引
--   2. DROP engagement 表（生产代码已不读写）
--   3. ALTER owner_type/owner_id SET NOT NULL（强制后续行必填 owner）

-- 1. DROP engagement_id 列
ALTER TABLE finding DROP COLUMN engagement_id;
ALTER TABLE agent_run DROP COLUMN engagement_id;
ALTER TABLE llm_invocation DROP COLUMN engagement_id;
ALTER TABLE http_flow DROP COLUMN engagement_id;

-- 2. DROP engagement 表 + 其唯一索引
DROP INDEX IF EXISTS engagement_active_passive_uniq;
DROP INDEX IF EXISTS engagement_expires_idx;
DROP TABLE engagement;

-- 3. owner_type/owner_id 升级为 NOT NULL（4 张共享表）
--    经 B6.2/B6.3 切换后所有新行都带 owner；旧行已通过 owner_id OR 兼容查询读到。
--    若仍有 owner_type/owner_id 为 NULL 的行（如 0040 之前的旧数据），本 ALTER 会失败——
--    需先 UPDATE 给 NULL 行赋值，或 DELETE 这些行。
ALTER TABLE finding ALTER COLUMN owner_type SET NOT NULL;
ALTER TABLE finding ALTER COLUMN owner_id SET NOT NULL;
ALTER TABLE agent_run ALTER COLUMN owner_type SET NOT NULL;
ALTER TABLE agent_run ALTER COLUMN owner_id SET NOT NULL;
ALTER TABLE llm_invocation ALTER COLUMN owner_type SET NOT NULL;
ALTER TABLE llm_invocation ALTER COLUMN owner_id SET NOT NULL;
-- http_flow 仅 passive_session_id 必填（active 不入此表，无 owner_type 列）
ALTER TABLE http_flow ALTER COLUMN passive_session_id SET NOT NULL;
