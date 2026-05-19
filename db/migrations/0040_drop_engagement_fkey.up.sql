-- 0040: DROP engagement_id FK constraints from 4 shared tables.
--
-- 设计动因：
--   - 0039 已 DROP NOT NULL，但 FK 仍约束 engagement_id 必须指向有效 engagement 行
--   - 下一步要 DROP engagement 表，需先解除所有引用约束
--   - 数据回填策略：用户授权清数据，不做回填——旧行 engagement_id 仍指向旧 engagement.id，
--     但 engagement 表行被 DROP 后这些值变"孤儿"无意义；查询通过 owner_id 路径走
--
-- 本 migration 仅 DROP FK，**不**删 engagement_id 列、**不**删 engagement 表（留 0041）：
--   - 旧 caller 仍可写 engagement_id（值不再被 FK 校验）
--   - 新 caller 写 NULL 也 OK（0039 DROP NOT NULL）
--   - 现有 finding/agent_run/llm_invocation/http_flow 行的 engagement_id 列值保留（向前兼容）

ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_engagement_id_fkey;
ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_engagement_id_fkey;
ALTER TABLE llm_invocation DROP CONSTRAINT IF EXISTS llm_invocation_engagement_id_fkey;
ALTER TABLE http_flow DROP CONSTRAINT IF EXISTS http_flow_engagement_id_fkey;
