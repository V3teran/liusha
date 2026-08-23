-- 0105 down：从能力档（heavy/vision/light）还原回 0104 的 per-agent 摊平行 + __default__。
--
-- 还原映射（0105 up 的逆）：
--   __default__      ← heavy   （heavy 承载旧 default_provider 语义）
--   traffic-analysis ← heavy
--   planner     ← vision
--   exploitation     ← vision
--   inspector        ← light
--   __fallback__     原样保留。
-- 再删掉三个 tier 行。幂等：ON CONFLICT DO NOTHING（若某 agent 行仍在则不覆盖）。

BEGIN;

INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT '__default__', provider_key, now(), now() FROM llm_role_route WHERE role = 'heavy'
ON CONFLICT (role) DO NOTHING;
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'traffic-analysis', provider_key, now(), now() FROM llm_role_route WHERE role = 'heavy'
ON CONFLICT (role) DO NOTHING;
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'planner', provider_key, now(), now() FROM llm_role_route WHERE role = 'vision'
ON CONFLICT (role) DO NOTHING;
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'exploitation', provider_key, now(), now() FROM llm_role_route WHERE role = 'vision'
ON CONFLICT (role) DO NOTHING;
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT 'inspector', provider_key, now(), now() FROM llm_role_route WHERE role = 'light'
ON CONFLICT (role) DO NOTHING;

DELETE FROM llm_role_route WHERE role IN ('heavy', 'vision', 'light');

COMMENT ON TABLE llm_role_route IS '角色 → provider 直连路由（拆别名层，0099）；保留 role __default__（未命中兜底）/ __fallback__（retry 备胎）';

COMMIT;
