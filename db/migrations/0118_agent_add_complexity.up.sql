-- 0118: agent 表添加 complexity 字段
--
-- 目的：每个 Agent 可配置其所需的 LLM 复杂度档位，实现精细化成本控制。
-- 不同 Agent 即使属于同一角色类型，也可能需要不同复杂度的推理能力。

ALTER TABLE agent
ADD COLUMN complexity TEXT
CHECK (complexity IN ('simple', 'medium', 'complex'))
DEFAULT 'medium';

-- 初始化现有 Agent 的 complexity（根据角色特性）
-- 规划类 Agent：需要深度推理和全局视角
UPDATE agent SET complexity = 'complex'
WHERE code LIKE '%planner%' OR code LIKE '%plan%' OR code LIKE '%strategy%';

-- 快速验证类 Agent：简单检查和确认
UPDATE agent SET complexity = 'simple'
WHERE code LIKE '%verify%' OR code LIKE '%check%' OR code LIKE '%scan%' OR code LIKE '%recon%';

-- 其余 Agent 保持默认 medium

COMMENT ON COLUMN agent.complexity IS '所需 LLM 复杂度：simple（快速任务）| medium（常规推理）| complex（深度分析）';
