-- 0099 down: 还原别名中间层（role → 别名 → provider 两跳）。
--
-- 逆向策略：重建 llm_alias 表，为每个被引用的 provider 造一个「同名于 provider key」的别名，
-- role_route 改回引用别名。保留 role '__default__'/'__fallback__' 还原成 default/fallback 别名。
-- 注：无法完美还原原始别名命名（light/vision 已在 up 里失去语义），故用 provider key 作别名名——
-- 这是 down 的最大努力（数据不丢，路由目标一致），语义命名需人工重配。

BEGIN;

-- 1) 重建别名表。
CREATE TABLE llm_alias (
    name         text        NOT NULL PRIMARY KEY,
    provider_key text        NOT NULL REFERENCES llm_provider(key) ON DELETE RESTRICT,
    description  text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE llm_alias IS 'LLM 命名别名 → provider（0099 down 还原）';

-- 2) 为 role_route 引用到的每个 provider 造一个同名别名（provider key 即别名名）。
INSERT INTO llm_alias (name, provider_key, description)
SELECT DISTINCT provider_key, provider_key, '0099 down 还原：自动生成'
FROM llm_role_route
ON CONFLICT (name) DO NOTHING;

-- 3) 保留 role 折回 default/fallback 别名。
INSERT INTO llm_alias (name, provider_key, description)
SELECT 'default', provider_key, '0099 down 还原'
FROM llm_role_route WHERE role = '__default__'
ON CONFLICT (name) DO NOTHING;

INSERT INTO llm_alias (name, provider_key, description)
SELECT 'fallback', provider_key, '0099 down 还原'
FROM llm_role_route WHERE role = '__fallback__'
ON CONFLICT (name) DO NOTHING;

-- 4) role_route 换回 alias 列（引用刚造的同名别名）。
ALTER TABLE llm_role_route ADD COLUMN alias text;
UPDATE llm_role_route SET alias = provider_key;

-- 5) 删掉保留 role 行（default/fallback 已回到别名表）。
DELETE FROM llm_role_route WHERE role IN ('__default__', '__fallback__');

-- 6) 换约束：删 provider_key FK + 列，alias 置 NOT NULL 指向 llm_alias。
ALTER TABLE llm_role_route DROP CONSTRAINT llm_role_route_provider_fk;
ALTER TABLE llm_role_route DROP COLUMN provider_key;
ALTER TABLE llm_role_route ALTER COLUMN alias SET NOT NULL;
ALTER TABLE llm_role_route
    ADD CONSTRAINT llm_role_route_alias_fkey
    FOREIGN KEY (alias) REFERENCES llm_alias(name) ON DELETE RESTRICT;

COMMENT ON TABLE llm_role_route IS '角色 → 别名路由；role 未命中走 default 别名';

COMMIT;
