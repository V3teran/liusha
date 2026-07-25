-- keyset 分页索引：审计列表查询恒为 WHERE task_id=? AND id > ? ORDER BY id ASC。
-- 原 (task_id) 单列索引只能过滤，取回的行仍需按 id 排序；(task_id, id) 复合索引让
-- 过滤 + 有序扫描一次走完（index-only 拿游标区间），翻页不随页数变慢。
CREATE INDEX llm_invocation_task_id_idx ON llm_invocation (task_id, id);

-- (task_id) 单列索引成为复合索引的前缀冗余（Postgres 可用复合索引的最左前缀），删掉省写入开销。
DROP INDEX IF EXISTS llm_invocation_task_idx;
