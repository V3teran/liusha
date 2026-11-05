-- 0194 down: 删目标防重唯一索引（去重迁移不回滚——已标 failed 的重复目标保持终态）。

DROP INDEX IF EXISTS exploration_objective_task_host_uniq;
