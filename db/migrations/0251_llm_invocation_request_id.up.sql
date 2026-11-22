-- LLM 调用删去 request_id：它是库侧生成的第二个 UUID，没有发给上游、没有写进日志、也没有
-- 按它查找的查询（详情按 bigint 主键 id 取），并不关联任何外部系统。调用的标识就是 id
-- （界面显示为「#编号」，URL 里的 selected 也是它）。连同它的索引一起删除。
DROP INDEX IF EXISTS llm_invocation_request_id_idx;
ALTER TABLE llm_invocation DROP COLUMN request_id;
