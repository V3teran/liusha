-- 回滚 0039：4 张共享表 engagement_id 列 SET NOT NULL。
--
-- 注意：若期间已写入 engagement_id IS NULL 的行（新 caller 切完之后），本 migration 会失败——
-- 需先 UPDATE 把 NULL 行补回（或 DELETE 这些行）。

ALTER TABLE finding ALTER COLUMN engagement_id SET NOT NULL;
ALTER TABLE agent_run ALTER COLUMN engagement_id SET NOT NULL;
ALTER TABLE llm_invocation ALTER COLUMN engagement_id SET NOT NULL;
ALTER TABLE http_flow ALTER COLUMN engagement_id SET NOT NULL;
