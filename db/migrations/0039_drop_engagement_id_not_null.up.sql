-- 0039: 4 张共享表 engagement_id 列 DROP NOT NULL。为后续 caller 不再写 engagement_id 做铺垫。
--
-- 设计动因：
--   - 0038 加 owner_type/owner_id 双轨列（nullable）；caller 双写
--   - 下个阶段 caller 全切到只写 owner_type/owner_id，engagement_id 留 NULL
--   - 当前 engagement_id 列仍 NOT NULL，会阻止"只写新列"的切换
--
-- 本 migration 仅放开 NOT NULL 约束，**不**删列、**不**删 FK：
--   - 旧 caller 继续传 engagement_id 仍生效（行为不变）
--   - 新 caller 可传 NULL（store SQL NULLIF 折空串到 NULL）
--   - 数据回填 / DROP engagement 表留 0040 完成（破坏式）

ALTER TABLE finding ALTER COLUMN engagement_id DROP NOT NULL;
ALTER TABLE agent_run ALTER COLUMN engagement_id DROP NOT NULL;
ALTER TABLE llm_invocation ALTER COLUMN engagement_id DROP NOT NULL;
ALTER TABLE http_flow ALTER COLUMN engagement_id DROP NOT NULL;
