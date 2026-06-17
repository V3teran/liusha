-- 0069：删除 llm_invocation.cost_usd 列。
--
-- liusha 不再统计/估算成本——各 provider 计价口径不一，cost 由各模型方自行结算，
-- liusha 只保留 token 用量（in/out/cached）。pricing 引擎（observability 包）已随代码移除。
-- IF EXISTS：全新库 0001 建列 → 本迁移删；幂等安全。
ALTER TABLE llm_invocation DROP COLUMN IF EXISTS cost_usd;
