-- 0113: executor → agent 命名统一（核心架构重构第一步）
--
-- 背景：将 executor 统一改名为 agent，kind 枚举从 planner/domain 改为 planner/executor。
-- Planner 升级为 LLM Agent 后入库（kind='planner'），4 个域 Executor（kind='executor'）。
-- executor_run 改名 agent_run，所有外键列统一。

-- ===== 表改名 =====

ALTER TABLE executor RENAME TO agent;
ALTER TABLE executor_run RENAME TO agent_run;

-- ===== kind 枚举修改 =====

-- 删除旧约束
ALTER TABLE agent DROP CONSTRAINT executor_kind_check;

-- 更新数据
UPDATE agent SET kind = 'planner' WHERE kind = 'planner';
UPDATE agent SET kind = 'executor' WHERE kind = 'domain';

-- 添加新约束
ALTER TABLE agent ADD CONSTRAINT agent_kind_check
    CHECK (kind IN ('planner', 'executor'));

-- ===== 列改名 =====

-- agent_run 表
ALTER TABLE agent_run RENAME COLUMN executor_id TO agent_id;

-- finding 表
ALTER TABLE finding RENAME COLUMN executor_run_id TO agent_run_id;

-- llm_invocation 表
ALTER TABLE llm_invocation RENAME COLUMN executor_run_id TO agent_run_id;

-- tool_invocation 表
ALTER TABLE tool_invocation RENAME COLUMN executor_run_id TO agent_run_id;

-- agent_traffic 表
ALTER TABLE agent_traffic RENAME COLUMN executor_run_id TO agent_run_id;

-- scenario 表
ALTER TABLE scenario RENAME COLUMN solo_executor_id TO solo_agent_id;

-- ===== 约束改名 =====

-- agent 表约束
ALTER TABLE agent RENAME CONSTRAINT executor_pkey TO agent_pkey;
ALTER TABLE agent RENAME CONSTRAINT executor_owner_type_check TO agent_owner_type_check;
ALTER TABLE agent RENAME CONSTRAINT executor_status_check TO agent_status_check;
ALTER TABLE agent RENAME CONSTRAINT executor_role_check TO agent_role_check;

-- agent_run 表约束
ALTER TABLE agent_run RENAME CONSTRAINT executor_run_pkey TO agent_run_pkey;
ALTER TABLE agent_run RENAME CONSTRAINT executor_run_executor_id_fkey TO agent_run_agent_id_fkey;

-- finding 表约束
ALTER TABLE finding RENAME CONSTRAINT finding_executor_run_id_fkey TO finding_agent_run_id_fkey;

-- llm_invocation 表约束
ALTER TABLE llm_invocation RENAME CONSTRAINT llm_invocation_executor_run_id_fkey TO llm_invocation_agent_run_id_fkey;

-- tool_invocation 表约束
ALTER TABLE tool_invocation RENAME CONSTRAINT tool_invocation_executor_run_id_fkey TO tool_invocation_agent_run_id_fkey;

-- agent_traffic 表约束
ALTER TABLE agent_traffic RENAME CONSTRAINT agent_traffic_executor_run_id_fkey TO agent_traffic_agent_run_id_fkey;

-- scenario 表约束
ALTER TABLE scenario RENAME CONSTRAINT scenario_solo_executor_id_fkey TO scenario_solo_agent_id_fkey;

-- ===== 索引改名 =====

ALTER INDEX executor_owner_idx RENAME TO agent_owner_idx;
ALTER INDEX executor_run_executor_id_idx RENAME TO agent_run_agent_id_idx;
-- agent_run_task_id_idx 保持不变（不含 executor 词）

-- ===== 注释更新 =====

COMMENT ON TABLE agent IS 'Agent 定义表：planner（规划型）+ executor（执行型，4 域：web/binary/cloud/lateral）';
COMMENT ON COLUMN agent.kind IS 'planner（规划型，读图产出 Move）| executor（执行型，执行 Move 产出 Attempt）';
COMMENT ON TABLE agent_run IS 'Agent 运行实例：一次 agent 执行的记录（Planner 规划 + Executor 执行）';
