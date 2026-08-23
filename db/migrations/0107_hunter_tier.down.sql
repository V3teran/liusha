-- 回滚 0107：移除 agent.tier 列（agent → 档位绑定回落 Go 代码 agentTierTable）。
ALTER TABLE agent DROP COLUMN tier;
