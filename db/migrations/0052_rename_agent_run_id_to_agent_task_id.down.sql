-- 0052 down: 回滚列名重命名。
-- 注意：回滚后 Go 代码会编译失败（SQL 仍用 agent_task_id），需要同时 git checkout
-- 到 0052 之前的 commit 才能跑起来。

ALTER TABLE finding RENAME COLUMN agent_task_id TO agent_run_id;
ALTER TABLE llm_invocation RENAME COLUMN agent_task_id TO agent_run_id;
