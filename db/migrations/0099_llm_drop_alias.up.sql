-- 0099: 拆掉 LLM 别名中间层——role → 别名 → provider 两跳压成 role → provider 一跳。
--
-- 动机（用户裁决）：别名层（llm_alias）是「换一个视觉模型改一处即全量跟随」的间接层，
-- 但实际只有 4 个语义槽（default/light/vision/fallback），且多个 role 指向同一 provider 时
-- 别名并未带来复用收益——它只是把「role→provider」拆成两张表两次编辑。对运维是纯认知负担：
-- 配一个 role 要先想「它走哪个别名」再想「那个别名绑哪个 provider」。直连后配置即所见。
--
-- 语义保全（不是删功能，是去中间层）：
--   · light/vision 别名本是「共享桶」——原先指向它们的 role 现直接指向对应 provider。
--   · default（role 未命中兜底）与 fallback（retry 耗尽备胎）是**全局**槽，不属于任何 role，
--     故降为两个保留 role 行：'__default__' / '__fallback__'，与普通 role 同表同解析。
--
-- 迁移策略：就地把 llm_role_route.alias 解引用成 provider_key（JOIN llm_alias），
-- 再把 default/fallback 别名折成保留 role 行，最后换 FK 指向 llm_provider、删 llm_alias。
-- 全程保数据：已有 role 路由的目标 provider 一个不丢。

BEGIN;

-- 1) 新增 provider_key 列（暂可空，回填后再置 NOT NULL）。
ALTER TABLE llm_role_route ADD COLUMN provider_key text;

-- 2) 把每条 role 路由的 alias 解引用成它当前绑定的 provider key。
UPDATE llm_role_route r
SET provider_key = a.provider_key
FROM llm_alias a
WHERE r.alias = a.name;

-- 3) 把全局 default / fallback 别名折成保留 role 行（若对应别名存在且该保留 role 尚未占用）。
--    '__default__'：role 未命中任何显式路由时的兜底（等价旧 default 别名）。
--    '__fallback__'：primary retry 耗尽后切换的备胎（等价旧 fallback 别名）。
INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT '__default__', a.provider_key, now(), now()
FROM llm_alias a
WHERE a.name = 'default'
ON CONFLICT (role) DO NOTHING;

INSERT INTO llm_role_route (role, provider_key, created_at, updated_at)
SELECT '__fallback__', a.provider_key, now(), now()
FROM llm_alias a
WHERE a.name = 'fallback'
ON CONFLICT (role) DO NOTHING;

-- 4) 删掉仍未回填 provider_key 的行（其 alias 已不存在——脏数据，直连模型下无意义）。
DELETE FROM llm_role_route WHERE provider_key IS NULL;

-- 5) 换约束：删旧 alias 列 + 其 FK，provider_key 置 NOT NULL 并指向 llm_provider。
ALTER TABLE llm_role_route DROP COLUMN alias;
ALTER TABLE llm_role_route ALTER COLUMN provider_key SET NOT NULL;
ALTER TABLE llm_role_route
    ADD CONSTRAINT llm_role_route_provider_fk
    FOREIGN KEY (provider_key) REFERENCES llm_provider(key) ON DELETE RESTRICT;

COMMENT ON TABLE llm_role_route IS '角色 → provider 直连路由（拆别名层，0099）；保留 role __default__（未命中兜底）/ __fallback__（retry 备胎）';
COMMENT ON COLUMN llm_role_route.provider_key IS 'provider 部署 key（直连，取代原 alias 间接层）';

-- 6) 别名层退休。
DROP TABLE llm_alias;

COMMIT;
