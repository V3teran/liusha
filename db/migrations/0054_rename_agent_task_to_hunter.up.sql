-- 0054: agent_task 表重命名为 agent，命名层级跟 v1.1 agent 小队架构对齐。
--
-- 命名设计（一次定型）：
--   - active_scan = 业务"扫描任务"（用户视角的 "task"）
--   - agent      = 每次 agent ReAct 运行的持久化记录（planner 或 exploitation）
--   - role enum   = traffic-analysis / planner / exploitation（inspector 旁路不入表）
--
-- 同步 rename：3 个 index + 4 个 constraint（跟旧表名 agent_task / agent_run 对齐过）。
-- 历史包袱：0050 migration 把 agent_run → agent_task，但 pkey 跟 owner_type_check 等
-- 约束当时没 rename，仍带 agent_run_* 前缀；本次彻底统一。

ALTER TABLE agent_task RENAME TO agent;

ALTER INDEX agent_run_pkey RENAME TO agent_pkey;
ALTER INDEX agent_task_planner_id_idx RENAME TO agent_planner_id_idx;
ALTER INDEX agent_task_owner_idx RENAME TO agent_owner_idx;

ALTER TABLE agent RENAME CONSTRAINT agent_run_owner_type_check TO agent_owner_type_check;
ALTER TABLE agent RENAME CONSTRAINT agent_run_status_check TO agent_status_check;
ALTER TABLE agent RENAME CONSTRAINT agent_task_role_check TO agent_role_check;
ALTER TABLE agent RENAME CONSTRAINT agent_task_planner_id_fkey TO agent_planner_id_fkey;
