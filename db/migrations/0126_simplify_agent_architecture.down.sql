-- 0125 回滚：恢复复杂的Agent架构
--
-- 警告：此回滚会丢失Planner和Executor的配置数据
-- 建议：迁移前备份数据

-- 1. 删除唯一约束
DROP INDEX IF EXISTS idx_agent_kind_enabled;

-- 2. 重命名字段
ALTER TABLE agent RENAME COLUMN system_prompt TO body;

-- 3. 删除新增字段
ALTER TABLE agent DROP COLUMN IF EXISTS skills;
ALTER TABLE agent DROP COLUMN IF EXISTS is_builtin;

-- 4. 注意：无法恢复scenario表和旧的多个executor
-- 如需恢复，请从备份中还原
