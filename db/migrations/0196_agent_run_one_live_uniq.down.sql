-- 0196 down: 撤销「每任务至多一个存活 run」部分唯一索引（清洗步不可逆，
-- 被标 aborted 的行不恢复）。
DROP INDEX IF EXISTS agent_run_one_live_per_task;
