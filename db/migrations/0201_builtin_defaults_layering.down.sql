-- 0201 回滚：恢复 agent / skill / system_setting 三张表的结构并删除分层表。
-- 只恢复结构，不恢复数据（迁移只进不退）；回滚后的旧版本启动时由其种子重新首填出厂配置，
-- 用户在新版本上的自定义不随回滚保留。跨此版本回滚前请先备份数据库。

DROP TABLE IF EXISTS public.agent_override;
DROP TABLE IF EXISTS public.agent_prompt_archive;
DROP TABLE IF EXISTS public.skill_override;
DROP TABLE IF EXISTS public.custom_skill;
DROP TABLE IF EXISTS public.setting_override;

CREATE TABLE public.agent (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    code text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    system_prompt text DEFAULT ''::text NOT NULL,
    function_tools jsonb DEFAULT '[]'::jsonb NOT NULL,
    max_iterations integer DEFAULT 40 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    cli_tools jsonb DEFAULT '[]'::jsonb NOT NULL,
    complexity text DEFAULT 'medium'::text NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    skills jsonb DEFAULT '[]'::jsonb NOT NULL,
    CONSTRAINT agent_complexity_check CHECK ((complexity = ANY (ARRAY['simple'::text, 'medium'::text, 'complex'::text])))
);

CREATE TABLE public.skill (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    code text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    name text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    body text DEFAULT ''::text NOT NULL,
    is_builtin boolean DEFAULT false NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE public.system_setting (
    group_key text NOT NULL,
    value jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT system_setting_group_key_check CHECK ((group_key = ANY (ARRAY['compaction'::text, 'runtime'::text, 'proxy_filter'::text])))
);

ALTER TABLE ONLY public.agent
    ADD CONSTRAINT agent_code_key UNIQUE (code);
ALTER TABLE ONLY public.agent
    ADD CONSTRAINT agent_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.skill
    ADD CONSTRAINT skill_code_key UNIQUE (code);
ALTER TABLE ONLY public.skill
    ADD CONSTRAINT skill_pkey PRIMARY KEY (id);
ALTER TABLE ONLY public.system_setting
    ADD CONSTRAINT system_setting_pkey PRIMARY KEY (group_key);
CREATE INDEX idx_skill_category ON public.skill USING btree (category);
CREATE INDEX idx_skill_enabled ON public.skill USING btree (enabled) WHERE (enabled = true);
