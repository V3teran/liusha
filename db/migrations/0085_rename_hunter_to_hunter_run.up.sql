-- 0085: 运行记录表 agent → agent_run，让出 agent 名给配置表（见 plan D0/M1）。
-- 对象清单按当前 \d agent 实测：3 索引 + 4 约束
-- （owner_* 在 0074 已随列删除，task_idx / task_id_fkey 是 0074 新增）。
-- 其他表指向 agent 的 FK 名不随本次改（rename 被引用表不改约束名，PG 自动更新引用目标）。
ALTER TABLE agent RENAME TO agent_run;

ALTER INDEX agent_pkey                 RENAME TO agent_run_pkey;
ALTER INDEX agent_planner_id_idx  RENAME TO agent_run_planner_id_idx;
ALTER INDEX agent_task_idx             RENAME TO agent_run_task_idx;

ALTER TABLE agent_run RENAME CONSTRAINT agent_role_check           TO agent_run_role_check;
ALTER TABLE agent_run RENAME CONSTRAINT agent_status_check         TO agent_run_status_check;
ALTER TABLE agent_run RENAME CONSTRAINT agent_planner_id_fkey TO agent_run_planner_id_fkey;
ALTER TABLE agent_run RENAME CONSTRAINT agent_task_id_fkey         TO agent_run_task_id_fkey;
