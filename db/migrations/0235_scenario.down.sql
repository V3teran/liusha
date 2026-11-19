-- 0235 降级：去掉场景；技能退回 0201 的分层结构（尽力而为）。
--
-- 自建技能回 custom_skill；内置技能里改过的（含停用、删除）回 skill_override：每个字段记为整值
-- 自定义（基线未知），last_saved 为完整内容；旧结构没有删除，删除过的内置技能按停用保留。
-- 未改且启用的内置技能不需要行（读取时取出厂值）。

ALTER TABLE public.cron_schedule DROP COLUMN scenario;
ALTER TABLE public.task
    DROP CONSTRAINT task_scenario_snapshot_check,
    DROP COLUMN scenario,
    DROP COLUMN scenario_name,
    DROP COLUMN scenario_body;
DROP TABLE public.scenario;

CREATE TABLE public.skill_override (
    skill_code text NOT NULL PRIMARY KEY,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    last_saved jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT skill_override_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text))
);
COMMENT ON TABLE public.skill_override IS '内置技能的用户自定义层：只存被自定义的字段；出厂内容编译在程序里';
COMMENT ON COLUMN public.skill_override.last_saved IS '最后一次保存时的完整生效内容：内置技能下线后据此转为同 code 的自建技能';

CREATE TABLE public.custom_skill (
    code text NOT NULL PRIMARY KEY,
    category text DEFAULT ''::text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    body text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
COMMENT ON TABLE public.custom_skill IS '用户自建技能；与内置技能同 code 时自建优先';
COMMENT ON COLUMN public.custom_skill.category IS '知识域：vuln（漏洞知识）或 tool（工具使用知识）';

INSERT INTO public.custom_skill (code, category, name, description, body, enabled, created_at, updated_at)
SELECT code, category, name, description, body, enabled, created_at, updated_at
  FROM public.skill WHERE NOT is_builtin;

INSERT INTO public.skill_override (skill_code, fields, last_saved, updated_at)
SELECT code,
       jsonb_build_object('name', jsonb_build_object('value', name),
                          'description', jsonb_build_object('value', description),
                          'body', jsonb_build_object('value', body),
                          'enabled', jsonb_build_object('value', enabled AND deleted_at IS NULL)),
       jsonb_build_object('code', code, 'category', category, 'name', name, 'description', description,
                          'body', body, 'enabled', enabled AND deleted_at IS NULL, 'is_builtin', true),
       updated_at
  FROM public.skill
 WHERE is_builtin
   AND (NOT enabled OR deleted_at IS NOT NULL OR seed_hash IS NULL
        OR seed_hash <> encode(sha256(convert_to(name || chr(31) || description || chr(31) || body, 'UTF8')), 'hex'));

DROP TABLE public.skill;
