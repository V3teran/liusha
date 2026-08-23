-- 0105: 路由从「per-agent 摊平行」收敛到「能力分档（tier）」——agent → tier → provider 两跳。
--
-- 动机（用户裁决）：0099 把 role→别名→provider 拆成 role→provider 一跳，但代价是每个 agent 各占
-- 一行路由，且多个 agent 指向同一 provider 时纯属重复。真正的复用单元是**能力档**（重推理/多模态/
-- 省钱），不是 agent。故引入 tier 中间层：agent→tier 固定在代码（llmcfg.AgentTier，运行期不可改，
-- 消除 0099 抱怨的「配一个 role 要先想它走哪」认知负担），tier→provider 落库前端可配（换一个视觉
-- 模型只改 vision 一处即全量跟随）。
--
-- schema 不变：llm_role_route.role 列继续存字符串，只是取值从 agent 名（planner…）变为
-- tier 名（heavy/vision/light）+ 保留 role __fallback__。本迁移只搬数据，不动表结构。
--
-- 数据搬迁（保数据，幂等）：从旧 per-agent 行的 provider 派生三档，再清理旧行 + 废弃的 __default__。
--   heavy  ← __default__（旧 default_provider 语义，新隐式默认档）；缺失则退回 traffic-analysis。
--   vision ← planner（旧 vision_provider 语义）；缺失则退回 exploitation。
--   light  ← inspector（旧 light_provider 语义）。
--   __fallback__ 原样保留（全局备胎，语义不变）。

BEGIN;

-- heavy：优先 __default__，退回 traffic-analysis（两条 INSERT 按偏好序，ON CONFLICT DO NOTHING 保证首个命中即锁定）。
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'heavy', provider_key, now(), now() FROM llm_role_route WHERE role = '__default__'
ON CONFLICT (role) DO NOTHING;
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'heavy', provider_key, now(), now() FROM llm_role_route WHERE role = 'traffic-analysis'
ON CONFLICT (role) DO NOTHING;

-- vision：优先 planner，退回 exploitation。
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'vision', provider_key, now(), now() FROM llm_role_route WHERE role = 'planner'
ON CONFLICT (role) DO NOTHING;
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'vision', provider_key, now(), now() FROM llm_role_route WHERE role = 'exploitation'
ON CONFLICT (role) DO NOTHING;

-- light：inspector。
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'light', provider_key, now(), now() FROM llm_role_route WHERE role = 'inspector'
ON CONFLICT (role) DO NOTHING;

-- 清理旧 per-agent 行 + 废弃的 __default__（agent→tier 已固定在代码，__default__ 由 heavy 档承载）。
DELETE FROM llm_role_route
WHERE role IN ('planner', 'traffic-analysis', 'exploitation', 'inspector', '__default__');

COMMENT ON TABLE llm_role_route IS '能力分档路由（0105）：role 列存 tier 名 heavy/vision/light（agent→tier 固定在代码 llmcfg.AgentTier）或保留 role __fallback__（retry 备胎）';

COMMIT;
