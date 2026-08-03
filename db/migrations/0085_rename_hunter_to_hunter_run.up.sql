-- 0085: 运行记录表 hunter → hunter_run，让出 hunter 名给配置表（见 plan D0/M1）。
-- 对象清单按当前 \d hunter 实测：3 索引 + 4 约束
-- （owner_* 在 0074 已随列删除，task_idx / task_id_fkey 是 0074 新增）。
-- 其他表指向 hunter 的 FK 名不随本次改（rename 被引用表不改约束名，PG 自动更新引用目标）。
ALTER TABLE hunter RENAME TO hunter_run;

ALTER INDEX hunter_pkey                 RENAME TO hunter_run_pkey;
ALTER INDEX hunter_orchestrator_id_idx  RENAME TO hunter_run_orchestrator_id_idx;
ALTER INDEX hunter_task_idx             RENAME TO hunter_run_task_idx;

ALTER TABLE hunter_run RENAME CONSTRAINT hunter_role_check           TO hunter_run_role_check;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_status_check         TO hunter_run_status_check;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_orchestrator_id_fkey TO hunter_run_orchestrator_id_fkey;
ALTER TABLE hunter_run RENAME CONSTRAINT hunter_task_id_fkey         TO hunter_run_task_id_fkey;
