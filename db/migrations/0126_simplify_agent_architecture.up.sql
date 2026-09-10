-- 0125: 简化Agent架构 - 只保留Planner和Executor
--
-- 目标：
-- 1. Agent表结构改造（添加 is_builtin 和 skills 字段）
-- 2. 删除Scenario表（与Agent概念冗余）
-- 3. 简化为通用Executor（LLM本身具备全领域能力）
--
-- 注意：Agent 数据从 agents/*.md 种子文件加载（见 internal/config/seed/）

-- ==================== Agent表改造 ====================

-- 1. 添加新字段
ALTER TABLE agent ADD COLUMN IF NOT EXISTS is_builtin boolean NOT NULL DEFAULT false;
ALTER TABLE agent ADD COLUMN IF NOT EXISTS skills jsonb NOT NULL DEFAULT '[]';

-- 2. 重命名body为system_prompt（更准确的命名）
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 3. 添加唯一约束（每种kind只能有1个enabled的Agent）
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_kind_enabled
    ON agent (kind) WHERE enabled = true;

-- 4. 删除所有现有Agent（将从种子文件重新加载）
DELETE FROM agent;

-- ==================== 删除Scenario表 ====================

-- Scenario表与Agent概念冗余，且engine字段违反新架构
-- 新架构：所有任务统一走 Planner + Executor，不再需要solo/swarm选择
DROP TABLE IF EXISTS scenario CASCADE;

-- ==================== 添加注释 ====================

COMMENT ON TABLE agent IS 'Agent配置表：从 agents/*.md 种子文件加载（Planner、Executor、Evaluator）';
COMMENT ON COLUMN agent.kind IS 'Agent类型：planner（规划者）、executor（执行者）或 evaluator（评估者）';
COMMENT ON COLUMN agent.system_prompt IS 'Agent的System Prompt，定义其行为和能力';
COMMENT ON COLUMN agent.skills IS 'Skill code列表，Agent可访问的知识库范围（如 ["tooling/browser-use", "vuln/dom-xss"]）';
COMMENT ON COLUMN agent.function_tools IS 'LLM可直接调用的function calling工具';
COMMENT ON COLUMN agent.cli_tools IS '外部命令行工具';
COMMENT ON COLUMN agent.is_builtin IS '是否为内置Agent（内置Agent不可删除）';
COMMENT ON INDEX idx_agent_kind_enabled IS '确保每种kind只有一个enabled的Agent';
