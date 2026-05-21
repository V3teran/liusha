-- 回滚 0040：重新 ADD FK constraint。
--
-- 注意：
--   1. 若期间 engagement 表已被 DROP（0041+），本 migration 无法 ADD FK，需先回滚 0041
--   2. 若期间出现 engagement_id 指向已删除 engagement 行（dangling）的数据，ADD FK 会失败——
--      需先 UPDATE 把 dangling 行 engagement_id 改 NULL，或 DELETE 这些行

ALTER TABLE finding ADD CONSTRAINT finding_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
ALTER TABLE agent_run ADD CONSTRAINT agent_run_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
ALTER TABLE llm_invocation ADD CONSTRAINT llm_invocation_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
ALTER TABLE http_flow ADD CONSTRAINT http_flow_engagement_id_fkey
    FOREIGN KEY (engagement_id) REFERENCES engagement(id) ON DELETE CASCADE;
