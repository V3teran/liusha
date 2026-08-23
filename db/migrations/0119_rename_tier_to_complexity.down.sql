-- 0119 down: 回滚 Complexity 到 Tier

-- 1. 恢复 agent 表的 tier 列
ALTER TABLE agent
ADD COLUMN tier TEXT NOT NULL DEFAULT 'heavy'
CHECK (tier IN ('heavy', 'vision', 'light'));

-- 2. 根据 complexity 反向映射到 tier
UPDATE agent SET tier = 'light' WHERE complexity = 'simple';
UPDATE agent SET tier = 'heavy' WHERE complexity = 'medium';
UPDATE agent SET tier = 'vision' WHERE complexity = 'complex';

-- 3. 删除 complexity 列
ALTER TABLE agent DROP COLUMN complexity;

-- 4. 恢复注释
COMMENT ON COLUMN agent.tier IS '能力档位：heavy（重推理）| vision（多模态）| light（轻任务）';
COMMENT ON TABLE llm_role_route IS 'LLM 角色路由表：role（agent code 或 tier 档位）→ provider_key';
