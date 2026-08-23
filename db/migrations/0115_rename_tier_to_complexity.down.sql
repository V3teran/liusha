-- 回滚 0115: complexity → tier

-- 列改名
ALTER TABLE llm_config RENAME COLUMN complexity TO tier;

-- 恢复数据
UPDATE llm_config SET tier = 'planner' WHERE tier = 'complex';
UPDATE llm_config SET tier = 'executor' WHERE tier = 'medium';
UPDATE llm_config SET tier = 'inspector' WHERE tier = 'simple';

-- 恢复主键
ALTER TABLE llm_config DROP CONSTRAINT llm_config_pkey;
ALTER TABLE llm_config ADD PRIMARY KEY (tier);

-- 恢复 CHECK 约束
ALTER TABLE llm_config DROP CONSTRAINT IF EXISTS llm_config_complexity_check;
ALTER TABLE llm_config ADD CONSTRAINT llm_config_tier_check
    CHECK (tier IN ('inspector', 'executor', 'planner'));
