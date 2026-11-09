-- 0201 内置内容分层：出厂值编译进程序，数据库只存用户自定义。
--
-- agent、内置技能、系统设置的出厂值从此随程序发布，不再写入数据库；数据库只存
-- 用户改过的字段（agent_override / skill_override / setting_override）与用户自建技能
-- （custom_skill）。原 agent / skill / system_setting 三张表在本迁移内转换后删除。
--
-- 老数据逐字段判定：等于任一历史出厂值（下方对照表由 git 全部历史生成）→ 视为未改，
-- 升级后跟随新版；否则视为用户修改，转为自定义保留（基线未知）。名单字段以最接近的
-- 历史出厂名单为起点换算为新增与删除。提示词已拆分为章程与契约，改过的整份旧提示词
-- 无法自动拆分，原文归档到 agent_prompt_archive，由用户对照后决定是否重新应用。
-- 误判方向只会是「未改判为改过」，表现为多一个可一键恢复默认的自定义，不丢数据。
--
-- 本文件的历史对照表由 scripts/gen-migration-0201 生成。

-- ── 新表 ────────────────────────────────────────────────────────────

CREATE TABLE public.agent_override (
    agent_code text NOT NULL PRIMARY KEY,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_override_agent_code_check CHECK ((agent_code = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text]))),
    CONSTRAINT agent_override_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text))
);
COMMENT ON TABLE public.agent_override IS 'agent 的用户自定义层：只存被自定义的字段；出厂值编译在程序里';
COMMENT ON COLUMN public.agent_override.fields IS '字段名 → 自定义记录：整值替换 {value, baseline}，名单 {added, removed}';

CREATE TABLE public.agent_prompt_archive (
    agent_code text NOT NULL PRIMARY KEY,
    prompt text NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT agent_prompt_archive_agent_code_check CHECK ((agent_code = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text])))
);
COMMENT ON TABLE public.agent_prompt_archive IS '升级前用户自定义过的整份提示词：提示词拆分为章程与契约后无法自动拆分，归档原文待用户对照处理';

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

CREATE TABLE public.setting_override (
    group_key text NOT NULL PRIMARY KEY,
    fields jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT setting_override_group_key_check CHECK ((group_key = ANY (ARRAY['compaction'::text, 'runtime'::text, 'proxy_filter'::text]))),
    CONSTRAINT setting_override_fields_check CHECK ((jsonb_typeof(fields) = 'object'::text))
);
COMMENT ON TABLE public.setting_override IS '系统设置的用户自定义层：只存被自定义的字段；出厂默认 = 代码 + config.yaml 部署值';

-- ── 历史出厂值对照表（生成）────────────────────────────────────────────

CREATE TEMP TABLE migration_history (seq integer, resource text, key text, field text, value jsonb);
INSERT INTO migration_history (seq, resource, key, field, value) VALUES
-- @HISTORY@

CREATE TEMP TABLE migration_setting_fields (group_key text, field text);
INSERT INTO migration_setting_fields (group_key, field) VALUES
-- @SETTING_FIELDS@

-- ── 判定函数（会话临时，迁移结束即失效）────────────────────────────────

CREATE FUNCTION pg_temp.text_fp(t text) RETURNS jsonb LANGUAGE sql IMMUTABLE AS
$$ SELECT to_jsonb(md5(btrim(t, E' \t\r\n'))) $$;

CREATE FUNCTION pg_temp.is_historical(r text, k text, f text, v jsonb) RETURNS boolean LANGUAGE sql STABLE AS
$$ SELECT EXISTS (SELECT 1 FROM migration_history h WHERE h.resource = r AND h.key = k AND h.field = f AND h.value = v) $$;

-- 整值字段：等于历史出厂值 → NULL（不算自定义），否则 {value}（基线未知）。
CREATE FUNCTION pg_temp.replace_entry(r text, k text, f text, v jsonb, fingerprint jsonb) RETURNS jsonb LANGUAGE sql STABLE AS
$$ SELECT CASE WHEN v IS NULL OR pg_temp.is_historical(r, k, f, fingerprint) THEN NULL
               ELSE jsonb_build_object('value', v) END $$;

CREATE FUNCTION pg_temp.set_norm(a jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS
$$ SELECT COALESCE((SELECT jsonb_agg(DISTINCT e ORDER BY e) FROM jsonb_array_elements(a) e), '[]'::jsonb) $$;

-- 名单字段：等于任一历史出厂名单 → NULL；否则以最接近的历史名单（对称差最小，
-- 同距取较新）为起点，返回 {added, removed}。
CREATE FUNCTION pg_temp.set_entry(r text, k text, f text, cur jsonb) RETURNS jsonb LANGUAGE plpgsql STABLE AS
$$
DECLARE
    c jsonb := pg_temp.set_norm(cur);
    base jsonb;
    best integer;
    h record;
    d integer;
    added jsonb;
    removed jsonb;
BEGIN
    IF cur IS NULL OR jsonb_typeof(cur) <> 'array' THEN
        RETURN NULL;
    END IF;
    FOR h IN SELECT pg_temp.set_norm(value) AS v FROM migration_history
             WHERE resource = r AND key = k AND field = f ORDER BY seq LOOP
        IF h.v = c THEN
            RETURN NULL;
        END IF;
        d := (SELECT count(*) FROM jsonb_array_elements(c) e WHERE NOT h.v @> jsonb_build_array(e))
           + (SELECT count(*) FROM jsonb_array_elements(h.v) e WHERE NOT c @> jsonb_build_array(e));
        IF best IS NULL OR d < best THEN
            base := h.v;
            best := d;
        END IF;
    END LOOP;
    base := COALESCE(base, '[]'::jsonb);
    added := (SELECT jsonb_agg(e) FROM jsonb_array_elements(c) e WHERE NOT base @> jsonb_build_array(e));
    removed := (SELECT jsonb_agg(e) FROM jsonb_array_elements(base) e WHERE NOT c @> jsonb_build_array(e));
    RETURN jsonb_strip_nulls(jsonb_build_object('added', added, 'removed', removed));
END
$$;

-- ── agent ───────────────────────────────────────────────────────────

INSERT INTO public.agent_override (agent_code, fields)
SELECT code, fields FROM (
    SELECT a.code, jsonb_strip_nulls(jsonb_build_object(
        'description',    pg_temp.replace_entry('agent', a.code, 'description', to_jsonb(a.description), pg_temp.text_fp(a.description)),
        'max_iterations', pg_temp.replace_entry('agent', a.code, 'max_iterations', to_jsonb(a.max_iterations), to_jsonb(a.max_iterations)),
        'complexity',     pg_temp.replace_entry('agent', a.code, 'complexity', to_jsonb(a.complexity), to_jsonb(a.complexity)),
        'function_tools', pg_temp.set_entry('agent', a.code, 'function_tools', a.function_tools),
        'cli_tools',      pg_temp.set_entry('agent', a.code, 'cli_tools', a.cli_tools),
        'skills',         pg_temp.set_entry('agent', a.code, 'skills', a.skills)
    )) AS fields
    FROM public.agent a
    WHERE a.code = ANY (ARRAY['planner', 'executor', 'evaluator', 'monitor'])
) x
WHERE fields <> '{}'::jsonb;

INSERT INTO public.agent_prompt_archive (agent_code, prompt)
SELECT a.code, a.system_prompt
FROM public.agent a
WHERE a.code = ANY (ARRAY['planner', 'executor', 'evaluator', 'monitor'])
  AND btrim(a.system_prompt, E' \t\r\n') <> ''
  AND NOT pg_temp.is_historical('agent', a.code, 'system_prompt', pg_temp.text_fp(a.system_prompt));

-- ── 技能 ────────────────────────────────────────────────────────────

-- 仍在出厂内容中的内置技能：改过的字段转为用户层，last_saved 为当时的完整内容。
INSERT INTO public.skill_override (skill_code, fields, last_saved)
SELECT code, fields, last_saved FROM (
    SELECT s.code,
        jsonb_strip_nulls(jsonb_build_object(
            'name',        pg_temp.replace_entry('skill', s.code, 'name', to_jsonb(s.name), pg_temp.text_fp(s.name)),
            'description', pg_temp.replace_entry('skill', s.code, 'description', to_jsonb(s.description), pg_temp.text_fp(s.description)),
            'body',        pg_temp.replace_entry('skill', s.code, 'body', to_jsonb(s.body), pg_temp.text_fp(s.body)),
            'enabled',     CASE WHEN s.enabled THEN NULL ELSE jsonb_build_object('value', false) END
        )) AS fields,
        jsonb_build_object('code', s.code, 'category', s.category, 'name', s.name, 'description', s.description,
                           'body', s.body, 'enabled', s.enabled, 'is_builtin', true) AS last_saved
    FROM public.skill s
    WHERE s.is_builtin AND s.code = ANY ('@BUILTIN_SKILLS@')
) x
WHERE fields <> '{}'::jsonb;

-- 已不在出厂内容中的内置技能：改过的转为同 code 的自建技能，未改过的随旧表删除。
INSERT INTO public.custom_skill (code, category, name, description, body, enabled, created_at, updated_at)
SELECT s.code, s.category, s.name, s.description, s.body, s.enabled, s.created_at, s.updated_at
FROM public.skill s
WHERE s.is_builtin
  AND NOT (s.code = ANY ('@BUILTIN_SKILLS@'))
  AND NOT (pg_temp.is_historical('skill', s.code, 'name', pg_temp.text_fp(s.name))
       AND pg_temp.is_historical('skill', s.code, 'description', pg_temp.text_fp(s.description))
       AND pg_temp.is_historical('skill', s.code, 'body', pg_temp.text_fp(s.body)));

-- 自建技能原样迁移。
INSERT INTO public.custom_skill (code, category, name, description, body, enabled, created_at, updated_at)
SELECT s.code, s.category, s.name, s.description, s.body, s.enabled, s.created_at, s.updated_at
FROM public.skill s
WHERE NOT s.is_builtin;

-- ── 系统设置 ────────────────────────────────────────────────────────

-- 只转换当前仍存在的字段（已废弃的旧字段随旧表删除）；空值视为未设置。
INSERT INTO public.setting_override (group_key, fields)
SELECT group_key, fields FROM (
    SELECT st.group_key,
        COALESCE((
            SELECT jsonb_object_agg(e.k, e.entry) FROM (
                SELECT kv.key AS k,
                       CASE WHEN jsonb_typeof(kv.value) = 'array'
                            THEN pg_temp.set_entry('setting', st.group_key, kv.key, kv.value)
                            ELSE pg_temp.replace_entry('setting', st.group_key, kv.key, kv.value, kv.value)
                       END AS entry
                FROM jsonb_each(st.value) kv
                JOIN migration_setting_fields sf ON sf.group_key = st.group_key AND sf.field = kv.key
                WHERE jsonb_typeof(kv.value) <> 'null'
            ) e
            WHERE e.entry IS NOT NULL
        ), '{}'::jsonb) AS fields
    FROM public.system_setting st
) x
WHERE fields <> '{}'::jsonb;

-- ── 删除旧表 ────────────────────────────────────────────────────────

DROP TABLE public.agent;
DROP TABLE public.skill;
DROP TABLE public.system_setting;

DROP TABLE migration_history;
DROP TABLE migration_setting_fields;
