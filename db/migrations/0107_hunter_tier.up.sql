-- 0107: agent 表加 tier 列——把 agent → 能力档的绑定从 Go 代码（llmcfg.agentTierTable）
-- 搬到 DB，让用户在「智能体」页按 agent 单独切换归档（只影响该 agent）。
--
-- tier ∈ {heavy, vision, light}，与 llmcfg TierHeavy/TierVision/TierLight 对齐；
-- DEFAULT 'heavy'（隐式默认档，与 AgentTier 未命中回退一致）。
-- 非 agent 的路由 key（inspector/compactor）无 DB 行，继续走代码兜底表。
ALTER TABLE agent
    ADD COLUMN tier text NOT NULL DEFAULT 'heavy'
    CHECK (tier IN ('heavy', 'vision', 'light'));

-- 回填现有 agent 的当前档位（与搬迁前 agentTierTable 一致，避免行为漂移）：
--   planner / exploitation → vision（active 截图链路，多模态）
--   traffic-analysis / reconnaissance → heavy（DEFAULT 已覆盖，无需显式 UPDATE）
UPDATE agent SET tier = 'vision' WHERE code IN ('planner', 'exploitation');
