-- 0155: 删除 agent.kind 列
--
-- 认知循环按 code（planner/executor/evaluator/monitor 四个固定 code）读配置，
-- kind 在运行时主路径零消费；"每种 kind 唯一 enabled"的唯一性保护由 code UNIQUE
-- 天然覆盖（四角色就是四个固定 code）。角色语义由 code 与 charter 正文承载。

DROP INDEX IF EXISTS idx_agent_kind_enabled;
ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_kind_check;
ALTER TABLE agent DROP COLUMN IF EXISTS kind;
