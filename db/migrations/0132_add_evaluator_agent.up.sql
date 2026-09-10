-- 0132: 添加 Evaluator Agent Kind
--
-- 新架构需要三个 Agent：Planner、Executor、Evaluator
-- Evaluator 负责评估 Observation，验证结果，产生 Result
--
-- 注意：Evaluator 数据从 agents/evaluator.md 种子文件加载（见 internal/config/seed/）

-- 更新注释：Agent 表现在包含三个角色
COMMENT ON TABLE agent IS 'Agent 配置表：包含 Planner、Executor、Evaluator 三个固定角色';
COMMENT ON COLUMN agent.kind IS 'Agent 类型：planner（规划者）、executor（执行者）或 evaluator（评估者）';
