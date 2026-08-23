-- 0115: LLM 路由 tier → complexity 命名统一
--
-- 背景：tier 用内部模块名（planner/executor/inspector）不合理，
-- 改为业务复杂度（complex/medium/simple），对标任务难度而非模块名。

-- ===== llm_config 表 =====

-- 列改名
ALTER TABLE llm_config RENAME COLUMN tier TO complexity;

-- 更新数据
UPDATE llm_config SET complexity = 'complex' WHERE complexity = 'planner';
UPDATE llm_config SET complexity = 'medium' WHERE complexity = 'executor';
UPDATE llm_config SET complexity = 'simple' WHERE complexity = 'inspector';

-- 更新主键约束（如果 tier 是主键的一部分）
-- 假设 llm_config 的主键是 tier，需要重建
ALTER TABLE llm_config DROP CONSTRAINT llm_config_pkey;
ALTER TABLE llm_config ADD PRIMARY KEY (complexity);

-- 更新 CHECK 约束（如果有）
-- 假设有 tier 的 CHECK 约束
ALTER TABLE llm_config DROP CONSTRAINT IF EXISTS llm_config_tier_check;
ALTER TABLE llm_config ADD CONSTRAINT llm_config_complexity_check
    CHECK (complexity IN ('simple', 'medium', 'complex'));

-- ===== 注释更新 =====

COMMENT ON TABLE llm_config IS 'LLM 路由配置：按业务复杂度（simple/medium/complex）绑定模型';
COMMENT ON COLUMN llm_config.complexity IS '业务复杂度：simple（快速验证）| medium（标准执行）| complex（深度推理）';
