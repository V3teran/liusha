-- 0196_baseline：迁移链压缩基线（原 0001–0196 全链的一次性收口）。
--
-- 内容 = 空库跑完整链后 pg_dump --schema-only 的产物（extensions/表/索引/
-- 约束/生成列/触发器全量），保证基线 schema 与原链终态完全一致。唯一剥离：
-- pg_dump 序言的 search_path 会话设置（连接池下会毒化后续未限定 schema 的语句，
-- 所有对象本就 public 限定，无功能影响）。
-- 原链与逐条迁移的历史动机在 git 历史里（本次压缩之前的提交均可回溯）。
--
-- 语义：
--   - 全新环境：migrate up 跑本文件即得完整 schema（一次到位）。
--   - 已在 0196 的存量环境：ledger 记录 0196 == 本文件版本号，migrate no-op，
--     零触碰。
--   - ⚠ 部署版本低于 0196 的环境必须先用旧代码 make migrate 到 0196，再拉
--     本基线——否则 up 会对存量库重放全量建表。
--   - make migrate-down 语义随压缩变化：现在 down 一步=整库回卷（原为回卷
--     顶层一条），仅限开发库使用。

--
-- PostgreSQL database dump
--


-- Dumped from database version 17.10 (Debian 17.10-1.pgdg12+1)
-- Dumped by pg_dump version 17.10 (Debian 17.10-1.pgdg12+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

--
-- Name: pg_trgm; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;


--
-- Name: EXTENSION pg_trgm; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION pg_trgm IS 'text similarity measurement and index searching based on trigrams';


--
-- Name: pgcrypto; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;


--
-- Name: EXTENSION pgcrypto; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION pgcrypto IS 'cryptographic functions';


--
-- Name: vector; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;


--
-- Name: EXTENSION vector; Type: COMMENT; Schema: -; Owner: -
--

COMMENT ON EXTENSION vector IS 'vector data type and ivfflat and hnsw access methods';


--
-- Name: increment_exploration_node_version(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.increment_exploration_node_version() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.version = OLD.version + 1;
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;


--
-- Name: FUNCTION increment_exploration_node_version(); Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON FUNCTION public.increment_exploration_node_version() IS '自动递增 wm_node.version 字段';


--
-- Name: update_exploration_node_updated_at(); Type: FUNCTION; Schema: public; Owner: -
--

CREATE FUNCTION public.update_exploration_node_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$;


SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: account; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account (
    id bigint NOT NULL,
    username text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: TABLE account; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.account IS '控制面登录账号（单管理员）；初始账号经首启初始化向导建立';


--
-- Name: COLUMN account.password_hash; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.account.password_hash IS 'bcrypt PHC 格式哈希，cost 12';


--
-- Name: account_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.account_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: account_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.account_id_seq OWNED BY public.account.id;


--
-- Name: account_session; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.account_session (
    token_hash text NOT NULL,
    account_id bigint NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    renewed_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: TABLE account_session; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.account_session IS '登录会话：token 只存哈希；expires_at 滑动续期 + created_at 起 30 天绝对上限';


--
-- Name: agent; Type: TABLE; Schema: public; Owner: -
--

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


--
-- Name: TABLE agent; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.agent IS 'Agent 配置表：包含 Planner、Executor、Evaluator 三个固定角色';


--
-- Name: COLUMN agent.system_prompt; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent.system_prompt IS 'Agent的System Prompt，定义其行为和能力';


--
-- Name: COLUMN agent.function_tools; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent.function_tools IS 'LLM可直接调用的function calling工具';


--
-- Name: COLUMN agent.cli_tools; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent.cli_tools IS '外部命令行工具';


--
-- Name: COLUMN agent.complexity; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent.complexity IS '所需 LLM 复杂度档位：simple（快速响应）| medium（标准推理，默认）| complex（深度推理）';


--
-- Name: COLUMN agent.is_builtin; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent.is_builtin IS '是否为内置Agent（内置Agent不可删除）';


--
-- Name: COLUMN agent.skills; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent.skills IS 'Skill code列表，Agent可访问的知识库范围（如 ["tooling/browser-use", "vuln/dom-xss"]）';


--
-- Name: agent_run; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_run (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    role text NOT NULL,
    input jsonb DEFAULT '{}'::jsonb NOT NULL,
    result jsonb DEFAULT '{}'::jsonb NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    task_id uuid NOT NULL,
    CONSTRAINT agent_run_role_check CHECK ((role = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text]))),
    CONSTRAINT agent_run_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'running'::text, 'completed'::text, 'aborted'::text, 'error'::text])))
);


--
-- Name: TABLE agent_run; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.agent_run IS 'Agent 运行实例（不是任务定义，是具体的一次运行）';


--
-- Name: COLUMN agent_run.role; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.agent_run.role IS 'agent 角色枚举：traffic-analysis（passive 单 agent）/ planner（active 父）/ exploitation（active 子）。跟 asynq queue 路由（worker.RoleAgent）解耦。';


--
-- Name: agent_traffic; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_traffic (
    id bigint NOT NULL,
    task_id uuid NOT NULL,
    agent_run_id uuid,
    identity text,
    tool text,
    host text NOT NULL,
    method text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    request_headers jsonb,
    request_body bytea,
    response_headers jsonb,
    response_body bytea,
    duration_ms integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: TABLE agent_traffic; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.agent_traffic IS 'agent 自产流量（干活副产物/弹药，属 task，不触发分析）';


--
-- Name: agent_traffic_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.agent_traffic_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: agent_traffic_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.agent_traffic_id_seq OWNED BY public.agent_traffic.id;


--
-- Name: assignment; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.assignment (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    source text NOT NULL,
    payload jsonb DEFAULT '[]'::jsonb NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    schedule_id uuid,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    CONSTRAINT assignment_source_check CHECK ((source = ANY (ARRAY['self'::text, 'proxy'::text])))
);


--
-- Name: TABLE assignment; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.assignment IS '下发容器，所有任务的根';


--
-- Name: audit_log; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_log (
    id bigint NOT NULL,
    actor text NOT NULL,
    action text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: TABLE audit_log; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.audit_log IS '系统敏感操作审计：owner abort/create、credential 写删等';


--
-- Name: COLUMN audit_log.actor; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.audit_log.actor IS '示例: system / sweep / api_user:abcd1234 / scanner';


--
-- Name: COLUMN audit_log.action; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.audit_log.action IS '命名空间动词，示例: owner.abort / credential.set';


--
-- Name: audit_log_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.audit_log_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: audit_log_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.audit_log_id_seq OWNED BY public.audit_log.id;


--
-- Name: conversation; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.conversation (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    title text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    task_id uuid
);


--
-- Name: corpus; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.corpus (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    content text NOT NULL,
    tags text[] DEFAULT '{}'::text[] NOT NULL,
    source text NOT NULL,
    source_task_id uuid,
    embedding public.vector(1024),
    content_hash text NOT NULL,
    hit_count integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT corpus_content_check CHECK ((content <> ''::text)),
    CONSTRAINT corpus_source_check CHECK ((source = ANY (ARRAY['agent'::text, 'expert'::text])))
);


--
-- Name: cron_schedule; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.cron_schedule (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    cron_expr text NOT NULL,
    payload jsonb DEFAULT '[]'::jsonb NOT NULL,
    title text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    next_run_at timestamp with time zone,
    last_run_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    description text DEFAULT ''::text NOT NULL
);


--
-- Name: TABLE cron_schedule; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.cron_schedule IS '定时模板（P1 空表，P4 加 Scheduler）；每次触发克隆一个 assignment';


--
-- Name: exploration_edge; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.exploration_edge (
    task_id uuid NOT NULL,
    src_id uuid NOT NULL,
    rel text NOT NULL,
    dst_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_exploration_edge_rel CHECK ((rel = ANY (ARRAY['generates'::text, 'triggers'::text])))
);


--
-- Name: TABLE exploration_edge; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.exploration_edge IS '世界模型关系边表（5种关系）：GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON';


--
-- Name: COLUMN exploration_edge.rel; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.exploration_edge.rel IS '关系类型（全大写）：GENERATES(生成)/CONFIRMS(确认)/REFUTES(反驳)/ENABLES(使能)/DEPENDS_ON(依赖)';


--
-- Name: exploration_node; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.exploration_node (
    id uuid NOT NULL,
    task_id uuid NOT NULL,
    kind text NOT NULL,
    content jsonb DEFAULT '{}'::jsonb NOT NULL,
    state text,
    metadata jsonb DEFAULT '{}'::jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    CONSTRAINT ck_exploration_node_kind CHECK ((kind = ANY (ARRAY['objective'::text, 'action'::text, 'observation'::text, 'result'::text]))),
    CONSTRAINT ck_exploration_node_state CHECK (((state IS NULL) OR (state = ANY (ARRAY['open'::text, 'running'::text, 'completed'::text, 'failed'::text, 'exhausted'::text, 'aborted'::text])))),
    CONSTRAINT ck_state_by_kind CHECK ((((kind = ANY (ARRAY['action'::text, 'objective'::text])) AND (state IS NOT NULL)) OR (kind <> ALL (ARRAY['action'::text, 'objective'::text]))))
);


--
-- Name: TABLE exploration_node; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.exploration_node IS '世界模型节点表（5种类型）：objective/action/hypothesis/evidence/finding';


--
-- Name: COLUMN exploration_node.kind; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.exploration_node.kind IS '节点类型：objective(目标)/action(动作)/hypothesis(假设)/evidence(证据)/finding(发现)';


--
-- Name: COLUMN exploration_node.state; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.exploration_node.state IS 'action 专用：执行状态 open/blocked/running/done/failed/exhausted/aborted';


--
-- Name: COLUMN exploration_node.metadata; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.exploration_node.metadata IS 'GraphStore 元数据字段，存储业务特定属性';


--
-- Name: COLUMN exploration_node.version; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.exploration_node.version IS '乐观锁版本号，每次更新自增';


--
-- Name: exploration_verification; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.exploration_verification (
    id text NOT NULL,
    task_id uuid NOT NULL,
    node_id uuid NOT NULL,
    primitives jsonb DEFAULT '{}'::jsonb NOT NULL,
    outcome text NOT NULL,
    evidence jsonb DEFAULT '{}'::jsonb NOT NULL,
    duration_ms bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT ck_exploration_verification_outcome CHECK ((outcome = ANY (ARRAY['confirmed'::text, 'refuted'::text])))
);


--
-- Name: TABLE exploration_verification; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.exploration_verification IS '验证记录表（审计链）';


--
-- Name: COLUMN exploration_verification.outcome; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.exploration_verification.outcome IS '验证结果：confirmed(确认)/refuted(反驳)';


--
-- Name: finding; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.finding (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    agent_run_id uuid,
    severity text NOT NULL,
    target jsonb DEFAULT '{}'::jsonb NOT NULL,
    evaluation jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    host text DEFAULT ''::text NOT NULL,
    source_traffic_id bigint,
    summary text DEFAULT ''::text NOT NULL,
    cwe_id text,
    owasp_category text,
    first_seen_at timestamp with time zone DEFAULT now() NOT NULL,
    remediation text,
    task_id uuid NOT NULL,
    status text DEFAULT 'open'::text NOT NULL,
    triage_note text,
    triaged_at timestamp with time zone,
    seq bigint NOT NULL,
    repro jsonb,
    dedup_key text GENERATED ALWAYS AS (((((((((lower(host) || '|'::text) || lower(COALESCE(cwe_id, ''::text))) || '|'::text) || lower(regexp_replace(rtrim(split_part(COALESCE((target ->> 'path'::text), "substring"(summary, 1, 40)), '?'::text, 1), '/'::text), '/+'::text, '/'::text, 'g'::text))) || '|'::text) || lower(COALESCE((target ->> 'method'::text), ''::text))) || '|'::text) || lower(COALESCE((target ->> 'parameter'::text), ''::text)))) STORED,
    CONSTRAINT finding_status_check CHECK ((status = ANY (ARRAY['open'::text, 'confirmed'::text, 'fixed'::text, 'false_positive'::text, 'accepted'::text]))),
    CONSTRAINT finding_summary_max_500 CHECK ((length(summary) <= 500)),
    CONSTRAINT finding_summary_one_line CHECK ((summary !~ '\n'::text))
);


--
-- Name: COLUMN finding.severity; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.severity IS 'LLM 自由文本（建议 critical/high/medium/low/info 保持配色一致；其他值 UI 退化为蓝色）';


--
-- Name: COLUMN finding.summary; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.summary IS '漏洞自由文本描述（LLM 必填）：是什么、怎么验证、推理依据。替代旧的"主要靠 evidence jsonb 表达"模式，evidence 现退化为可选结构化补充。';


--
-- Name: COLUMN finding.cwe_id; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.cwe_id IS '示例值: CWE-89 (SQL Injection)';


--
-- Name: COLUMN finding.owasp_category; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.owasp_category IS '示例值: A03:2021 (Injection)';


--
-- Name: COLUMN finding.first_seen_at; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.first_seen_at IS '首次发现时间——Update 不变；created_at 保持一致';


--
-- Name: COLUMN finding.remediation; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.remediation IS '修复建议（自然语言）；evidence 仍存 PoC';


--
-- Name: COLUMN finding.status; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.status IS 'triage 处置态：open/confirmed/fixed/false_positive/accepted。write_finding 落库默认 open。';


--
-- Name: COLUMN finding.seq; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.seq IS '对外顺序号（bigserial，单调递增，报告/会话引用用）；内部主键仍是 id(uuid)';


--
-- Name: COLUMN finding.repro; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.repro IS '机器可验的复现配方（web={traffic_id,modifications,assert}）；喂 L4 Verifier 复现门自动晋升成世界模型节点。NULL=未产配方（仅人读记录，不自动晋升）';


--
-- Name: COLUMN finding.dedup_key; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.finding.dedup_key IS 'v3 公式：host + cwe_id + normalize_path(path) + method + parameter（防止假阳性）';


--
-- Name: finding_seq_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.finding_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: finding_seq_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.finding_seq_seq OWNED BY public.finding.seq;


--
-- Name: llm_invocation; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.llm_invocation (
    id bigint NOT NULL,
    agent_run_id uuid,
    provider text NOT NULL,
    model text NOT NULL,
    in_tokens integer DEFAULT 0 NOT NULL,
    out_tokens integer DEFAULT 0 NOT NULL,
    cached_tokens integer DEFAULT 0 NOT NULL,
    latency_ms integer DEFAULT 0 NOT NULL,
    finish_reason text DEFAULT ''::text NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    role text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    messages jsonb DEFAULT '[]'::jsonb NOT NULL,
    result jsonb DEFAULT '{}'::jsonb NOT NULL,
    task_id uuid,
    request_id text DEFAULT (gen_random_uuid())::text NOT NULL,
    error_kind text DEFAULT ''::text NOT NULL
);


--
-- Name: COLUMN llm_invocation.error_kind; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.llm_invocation.error_kind IS '错误分类：rate_limited|network|server|error（空=成功或未分类）；应用层按框架错误分类判定';


--
-- Name: llm_invocation_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.llm_invocation_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: llm_invocation_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.llm_invocation_id_seq OWNED BY public.llm_invocation.id;


--
-- Name: llm_provider; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.llm_provider (
    code text NOT NULL,
    type text NOT NULL,
    base_url text NOT NULL,
    default_model text NOT NULL,
    api_key_env text,
    max_tokens integer DEFAULT 4096 NOT NULL,
    supports_tools boolean DEFAULT true NOT NULL,
    supports_vision boolean NOT NULL,
    context_window integer NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    encrypted_api_key bytea,
    api_key_last4 text,
    thinking_budget_tokens bigint DEFAULT 0 NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    CONSTRAINT llm_provider_context_window_check CHECK ((context_window > 0)),
    CONSTRAINT llm_provider_type_check CHECK ((type = ANY (ARRAY['openai_compat'::text, 'anthropic'::text])))
);


--
-- Name: TABLE llm_provider; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.llm_provider IS 'LLM provider 部署注册表（取代 config.yaml providers:）；DB 为事实源，运行期多级缓存热读';


--
-- Name: COLUMN llm_provider.api_key_env; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.llm_provider.api_key_env IS '旧模式：环境变量名（仅当 encrypted_api_key 为空时生效的回退路径）';


--
-- Name: COLUMN llm_provider.encrypted_api_key; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.llm_provider.encrypted_api_key IS 'AES-256-GCM 密文（nonce||ciphertext），密钥来自 LIUSHA_LLM_KEY_SECRET；为空时回退 api_key_env 环境变量（旧数据兼容）';


--
-- Name: COLUMN llm_provider.api_key_last4; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.llm_provider.api_key_last4 IS 'API Key 明文末 4 位（脱敏辨识用，非密钥值本身）；为空表示未知（旧行或仅 ENV 回退）';


--
-- Name: llm_role_route; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.llm_role_route (
    role text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    provider_code text NOT NULL
);


--
-- Name: TABLE llm_role_route; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.llm_role_route IS 'LLM 角色路由表：role（agent code 或 complexity 档位）→ provider_key';


--
-- Name: COLUMN llm_role_route.provider_code; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.llm_role_route.provider_code IS 'provider 部署 key（直连，取代原 alias 间接层）';


--
-- Name: message; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.message (
    seq bigint NOT NULL,
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    conversation_id uuid NOT NULL,
    role text NOT NULL,
    kind text DEFAULT 'message'::text NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    metadata jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT message_kind_check CHECK ((kind = ANY (ARRAY['message'::text, 'event'::text]))),
    CONSTRAINT message_role_check CHECK ((role = ANY (ARRAY['user'::text, 'assistant'::text, 'system'::text, 'tool'::text])))
);


--
-- Name: message_seq_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.message_seq_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: message_seq_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.message_seq_seq OWNED BY public.message.seq;


--
-- Name: proxy_traffic; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.proxy_traffic (
    id bigint NOT NULL,
    host text NOT NULL,
    method text NOT NULL,
    scheme text DEFAULT ''::text NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    path text DEFAULT ''::text NOT NULL,
    status_code integer DEFAULT 0 NOT NULL,
    captured_at timestamp with time zone DEFAULT now() NOT NULL,
    request_raw bytea,
    response_raw bytea,
    content_type text DEFAULT ''::text NOT NULL,
    http_version text DEFAULT ''::text NOT NULL,
    resp_len bigint DEFAULT 0 NOT NULL
);


--
-- Name: TABLE proxy_traffic; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.proxy_traffic IS '代理捕获的真实流量（被分析输入，属 host，先于 task）';


--
-- Name: COLUMN proxy_traffic.request_raw; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.proxy_traffic.request_raw IS '完整请求报文文本（request-line + 头 + body），展示/replay 单一源';


--
-- Name: COLUMN proxy_traffic.response_raw; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.proxy_traffic.response_raw IS '完整响应报文文本（status-line + 头 + body），body 已解压/截断';


--
-- Name: COLUMN proxy_traffic.content_type; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.proxy_traffic.content_type IS '响应 Content-Type 主类型（前端 Pretty 美化判定）';


--
-- Name: COLUMN proxy_traffic.http_version; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.proxy_traffic.http_version IS '协议版本 token（HTTP/1.1 等），忠实重建 request/status line';


--
-- Name: COLUMN proxy_traffic.resp_len; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.proxy_traffic.resp_len IS '响应体字节数（已解压/截断后），前端流量列表「长度」列';


--
-- Name: proxy_traffic_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.proxy_traffic_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: proxy_traffic_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.proxy_traffic_id_seq OWNED BY public.proxy_traffic.id;


--
-- Name: skill; Type: TABLE; Schema: public; Owner: -
--

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


--
-- Name: TABLE skill; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.skill IS 'Skill 配置表：Agent 可访问的知识库文档';


--
-- Name: COLUMN skill.code; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.skill.code IS 'Skill 唯一标识，如 tooling/browser-use, vuln/dom-xss';


--
-- Name: COLUMN skill.category; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.skill.category IS 'Skill 知识域：vuln（漏洞知识）或 tool（工具使用知识）';


--
-- Name: COLUMN skill.body; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.skill.body IS 'Skill 正文内容（Markdown 格式）';


--
-- Name: COLUMN skill.is_builtin; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.skill.is_builtin IS '是否为内置 Skill（内置不可删除）';


--
-- Name: system_setting; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.system_setting (
    group_key text NOT NULL,
    value jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT system_setting_group_key_check CHECK ((group_key = ANY (ARRAY['compaction'::text, 'runtime'::text, 'proxy_filter'::text])))
);


--
-- Name: TABLE system_setting; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.system_setting IS '系统业务旋钮（分组 KV，DB 为事实源，运行期多级缓存热读）；基础设施参数不入此表';


--
-- Name: COLUMN system_setting.group_key; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.system_setting.group_key IS '分组键：compaction（会话压缩）/ runtime（工具运行时+沙箱+会话）/ proxy_filter（代理流量过滤规则）';


--
-- Name: COLUMN system_setting.value; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.system_setting.value IS '该分组类型化快照的 JSONB；settingstore 按 group 反序列化为 struct';


--
-- Name: task; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    brief text DEFAULT ''::text NOT NULL,
    target_host text DEFAULT ''::text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL,
    heartbeat_at timestamp with time zone DEFAULT now() NOT NULL,
    paused_ms bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    ended_at timestamp with time zone,
    error_message text DEFAULT ''::text NOT NULL,
    assignment_id uuid NOT NULL,
    paused boolean DEFAULT false NOT NULL,
    CONSTRAINT task_status_check CHECK ((status = ANY (ARRAY['active'::text, 'completed'::text, 'aborted'::text])))
);


--
-- Name: TABLE task; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.task IS '统一扫描任务，不再区分场景';


--
-- Name: COLUMN task.brief; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task.brief IS 'active：用户自然语言 brief；passive：空';


--
-- Name: COLUMN task.target_host; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task.target_host IS 'active 可空；passive 必填（被分析 host）';


--
-- Name: COLUMN task.paused_ms; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task.paused_ms IS 'FollowUp 历次停顿累计 ms；WallclockMs 减去它 = 纯 agent 耗时';


--
-- Name: COLUMN task.assignment_id; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task.assignment_id IS '所属 assignment（NOT NULL 强外键，无孤儿 task）';


--
-- Name: COLUMN task.paused; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task.paused IS '暂停中（pause 控制事件生效、未 resume）；仅 status=active 时有意义';


--
-- Name: task_control_event; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_control_event (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    task_id uuid NOT NULL,
    command text NOT NULL,
    payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    processed_at timestamp with time zone,
    status text DEFAULT 'pending'::text NOT NULL,
    error text,
    CONSTRAINT task_control_event_status_check CHECK ((status = ANY (ARRAY['pending'::text, 'processed'::text, 'failed'::text])))
);


--
-- Name: TABLE task_control_event; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.task_control_event IS '人工干预事件（控制平面 → Planner Agent）';


--
-- Name: COLUMN task_control_event.command; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task_control_event.command IS '控制命令：adjust_goal（调整目标）、inject_move（注入 Move）、pause（暂停）、resume（恢复）、terminate（终止）';


--
-- Name: COLUMN task_control_event.payload; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task_control_event.payload IS '命令参数：adjust_goal{new_goal}, inject_move{move_kind,target_ref,reason,priority}';


--
-- Name: COLUMN task_control_event.status; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.task_control_event.status IS '处理状态：pending（待处理）、processed（已处理）、failed（处理失败）';


--
-- Name: tool; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool (
    name text NOT NULL,
    kind text NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    synced_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT tool_kind_check CHECK ((kind = ANY (ARRAY['function'::text, 'cli'::text])))
);


--
-- Name: TABLE tool; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.tool IS '工具目录（内置 function + 外置 cli）；代码为事实源，启动期幂等同步的可查询副本';


--
-- Name: COLUMN tool.kind; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.tool.kind IS 'function=进程内原生函数工具；cli=tools.yaml 外置沙箱工具';


--
-- Name: tool_invocation; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.tool_invocation (
    id bigint NOT NULL,
    agent_run_id uuid NOT NULL,
    tool_name text NOT NULL,
    args jsonb DEFAULT '{}'::jsonb NOT NULL,
    output_size integer DEFAULT 0 NOT NULL,
    output_preview text DEFAULT ''::text NOT NULL,
    duration_ms integer DEFAULT 0 NOT NULL,
    error_message text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    task_id uuid NOT NULL,
    role text DEFAULT ''::text NOT NULL
);


--
-- Name: TABLE tool_invocation; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.tool_invocation IS '每次 ReAct 工具调用的 telemetry：哪个 tool / 参数 / 耗时 / 输出大小 / 错误';


--
-- Name: COLUMN tool_invocation.output_preview; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.tool_invocation.output_preview IS 'output 首 4096 字符；完整 output 在 LLM message history 里';


--
-- Name: tool_invocation_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.tool_invocation_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: tool_invocation_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.tool_invocation_id_seq OWNED BY public.tool_invocation.id;


--
-- Name: traffic_task; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.traffic_task (
    traffic_id bigint NOT NULL,
    task_id uuid NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: TABLE traffic_task; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON TABLE public.traffic_task IS '代理流量↔passive task 多对多消费关系（一条捕获可被多 task 复用）';


--
-- Name: account id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account ALTER COLUMN id SET DEFAULT nextval('public.account_id_seq'::regclass);


--
-- Name: agent_traffic id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_traffic ALTER COLUMN id SET DEFAULT nextval('public.agent_traffic_id_seq'::regclass);


--
-- Name: audit_log id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log ALTER COLUMN id SET DEFAULT nextval('public.audit_log_id_seq'::regclass);


--
-- Name: finding seq; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.finding ALTER COLUMN seq SET DEFAULT nextval('public.finding_seq_seq'::regclass);


--
-- Name: llm_invocation id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_invocation ALTER COLUMN id SET DEFAULT nextval('public.llm_invocation_id_seq'::regclass);


--
-- Name: message seq; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message ALTER COLUMN seq SET DEFAULT nextval('public.message_seq_seq'::regclass);


--
-- Name: proxy_traffic id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.proxy_traffic ALTER COLUMN id SET DEFAULT nextval('public.proxy_traffic_id_seq'::regclass);


--
-- Name: tool_invocation id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_invocation ALTER COLUMN id SET DEFAULT nextval('public.tool_invocation_id_seq'::regclass);


--
-- Name: account account_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account
    ADD CONSTRAINT account_pkey PRIMARY KEY (id);


--
-- Name: account_session account_session_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_session
    ADD CONSTRAINT account_session_pkey PRIMARY KEY (token_hash);


--
-- Name: account account_username_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account
    ADD CONSTRAINT account_username_key UNIQUE (username);


--
-- Name: agent agent_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent
    ADD CONSTRAINT agent_code_key UNIQUE (code);


--
-- Name: agent agent_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent
    ADD CONSTRAINT agent_pkey PRIMARY KEY (id);


--
-- Name: agent_run agent_run_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_run
    ADD CONSTRAINT agent_run_pkey PRIMARY KEY (id);


--
-- Name: agent_traffic agent_traffic_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_traffic
    ADD CONSTRAINT agent_traffic_pkey PRIMARY KEY (id);


--
-- Name: assignment assignment_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment
    ADD CONSTRAINT assignment_pkey PRIMARY KEY (id);


--
-- Name: audit_log audit_log_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_log
    ADD CONSTRAINT audit_log_pkey PRIMARY KEY (id);


--
-- Name: conversation conversation_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.conversation
    ADD CONSTRAINT conversation_pkey PRIMARY KEY (id);


--
-- Name: corpus corpus_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.corpus
    ADD CONSTRAINT corpus_pkey PRIMARY KEY (id);


--
-- Name: cron_schedule cron_schedule_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.cron_schedule
    ADD CONSTRAINT cron_schedule_pkey PRIMARY KEY (id);


--
-- Name: exploration_edge exploration_edge_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.exploration_edge
    ADD CONSTRAINT exploration_edge_pkey PRIMARY KEY (task_id, src_id, rel, dst_id);


--
-- Name: exploration_node exploration_node_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.exploration_node
    ADD CONSTRAINT exploration_node_pkey PRIMARY KEY (id);


--
-- Name: exploration_verification exploration_verification_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.exploration_verification
    ADD CONSTRAINT exploration_verification_pkey PRIMARY KEY (id);


--
-- Name: finding finding_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.finding
    ADD CONSTRAINT finding_pkey PRIMARY KEY (id);


--
-- Name: finding finding_seq_unique; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.finding
    ADD CONSTRAINT finding_seq_unique UNIQUE (seq);


--
-- Name: llm_invocation llm_invocation_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_invocation
    ADD CONSTRAINT llm_invocation_pkey PRIMARY KEY (id);


--
-- Name: llm_provider llm_provider_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_provider
    ADD CONSTRAINT llm_provider_pkey PRIMARY KEY (code);


--
-- Name: llm_role_route llm_role_route_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_role_route
    ADD CONSTRAINT llm_role_route_pkey PRIMARY KEY (role);


--
-- Name: message message_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message
    ADD CONSTRAINT message_pkey PRIMARY KEY (seq);


--
-- Name: proxy_traffic proxy_traffic_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.proxy_traffic
    ADD CONSTRAINT proxy_traffic_pkey PRIMARY KEY (id);


--
-- Name: skill skill_code_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill
    ADD CONSTRAINT skill_code_key UNIQUE (code);


--
-- Name: skill skill_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill
    ADD CONSTRAINT skill_pkey PRIMARY KEY (id);


--
-- Name: system_setting system_setting_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.system_setting
    ADD CONSTRAINT system_setting_pkey PRIMARY KEY (group_key);


--
-- Name: task_control_event task_control_event_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_control_event
    ADD CONSTRAINT task_control_event_pkey PRIMARY KEY (id);


--
-- Name: task task_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task
    ADD CONSTRAINT task_pkey PRIMARY KEY (id);


--
-- Name: tool_invocation tool_invocation_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_invocation
    ADD CONSTRAINT tool_invocation_pkey PRIMARY KEY (id);


--
-- Name: tool tool_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool
    ADD CONSTRAINT tool_pkey PRIMARY KEY (name);


--
-- Name: traffic_task traffic_task_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_task
    ADD CONSTRAINT traffic_task_pkey PRIMARY KEY (traffic_id, task_id);


--
-- Name: account_session_account_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX account_session_account_idx ON public.account_session USING btree (account_id);


--
-- Name: agent_run_one_live_per_task; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX agent_run_one_live_per_task ON public.agent_run USING btree (task_id) WHERE (status = ANY (ARRAY['pending'::text, 'running'::text]));


--
-- Name: agent_run_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_run_task_idx ON public.agent_run USING btree (task_id, created_at DESC);


--
-- Name: agent_traffic_identity_tool_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_traffic_identity_tool_idx ON public.agent_traffic USING btree (task_id, identity, tool);


--
-- Name: agent_traffic_path_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_traffic_path_idx ON public.agent_traffic USING btree (host, path);


--
-- Name: agent_traffic_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX agent_traffic_task_idx ON public.agent_traffic USING btree (task_id, created_at);


--
-- Name: assignment_schedule_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX assignment_schedule_idx ON public.assignment USING btree (schedule_id) WHERE (schedule_id IS NOT NULL);


--
-- Name: audit_log_action_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_action_idx ON public.audit_log USING btree (action, created_at DESC);


--
-- Name: audit_log_actor_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_actor_idx ON public.audit_log USING btree (actor, created_at DESC);


--
-- Name: audit_log_target_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX audit_log_target_idx ON public.audit_log USING btree (target_kind, target_id, created_at DESC);


--
-- Name: conversation_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX conversation_task_idx ON public.conversation USING btree (task_id) WHERE (task_id IS NOT NULL);


--
-- Name: corpus_content_hash_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX corpus_content_hash_uniq ON public.corpus USING btree (content_hash);


--
-- Name: corpus_content_trgm; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX corpus_content_trgm ON public.corpus USING gin (content public.gin_trgm_ops);


--
-- Name: corpus_embedding_hnsw; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX corpus_embedding_hnsw ON public.corpus USING hnsw (embedding public.vector_cosine_ops);


--
-- Name: corpus_tags_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX corpus_tags_gin ON public.corpus USING gin (tags);


--
-- Name: corpus_title_trgm; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX corpus_title_trgm ON public.corpus USING gin (title public.gin_trgm_ops);


--
-- Name: cron_schedule_due_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX cron_schedule_due_idx ON public.cron_schedule USING btree (next_run_at) WHERE enabled;


--
-- Name: exploration_objective_task_host_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX exploration_objective_task_host_uniq ON public.exploration_node USING btree (task_id, ((content ->> 'target_host'::text))) WHERE ((kind = 'objective'::text) AND (content ? 'target_host'::text));


--
-- Name: finding_source_traffic_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX finding_source_traffic_idx ON public.finding USING btree (source_traffic_id) WHERE (source_traffic_id IS NOT NULL);


--
-- Name: finding_status_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX finding_status_idx ON public.finding USING btree (status);


--
-- Name: finding_task_dedup_v3_uniq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX finding_task_dedup_v3_uniq ON public.finding USING btree (task_id, dedup_key);


--
-- Name: finding_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX finding_task_idx ON public.finding USING btree (task_id, created_at DESC);


--
-- Name: idx_agent_run_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_run_status ON public.agent_run USING btree (status);


--
-- Name: idx_agent_run_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_run_task_id ON public.agent_run USING btree (task_id);


--
-- Name: idx_agent_traffic_agent_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_traffic_agent_run_id ON public.agent_traffic USING btree (agent_run_id);


--
-- Name: idx_expl_edge_dst; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_edge_dst ON public.exploration_edge USING btree (task_id, dst_id);


--
-- Name: idx_expl_edge_rel; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_edge_rel ON public.exploration_edge USING btree (task_id, rel);


--
-- Name: idx_expl_node_content_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_node_content_gin ON public.exploration_node USING gin (content);


--
-- Name: idx_expl_node_metadata_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_node_metadata_gin ON public.exploration_node USING gin (metadata);


--
-- Name: idx_expl_node_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_node_task_id ON public.exploration_node USING btree (task_id);


--
-- Name: idx_expl_node_task_kind; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_node_task_kind ON public.exploration_node USING btree (task_id, kind);


--
-- Name: idx_expl_node_task_kind_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_node_task_kind_state ON public.exploration_node USING btree (task_id, kind, state) WHERE (state = ANY (ARRAY['running'::text, 'open'::text, 'blocked'::text]));


--
-- Name: idx_expl_node_task_state; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_node_task_state ON public.exploration_node USING btree (task_id, state) WHERE (state IS NOT NULL);


--
-- Name: idx_expl_verification_node_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_verification_node_id ON public.exploration_verification USING btree (node_id);


--
-- Name: idx_expl_verification_outcome; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_verification_outcome ON public.exploration_verification USING btree (task_id, outcome);


--
-- Name: idx_expl_verification_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_expl_verification_task_id ON public.exploration_verification USING btree (task_id);


--
-- Name: idx_finding_agent_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_finding_agent_run_id ON public.finding USING btree (agent_run_id);


--
-- Name: idx_finding_severity_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_finding_severity_status ON public.finding USING btree (severity, status) WHERE (status = ANY (ARRAY['open'::text, 'confirmed'::text]));


--
-- Name: idx_finding_target_gin; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_finding_target_gin ON public.finding USING gin (target);


--
-- Name: idx_finding_target_host; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_finding_target_host ON public.finding USING btree (((target ->> 'host'::text)));


--
-- Name: idx_finding_task_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_finding_task_created ON public.finding USING btree (task_id, created_at DESC);


--
-- Name: idx_llm_role_route_provider_code; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_llm_role_route_provider_code ON public.llm_role_route USING btree (provider_code);


--
-- Name: idx_skill_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_category ON public.skill USING btree (category);


--
-- Name: idx_skill_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_enabled ON public.skill USING btree (enabled) WHERE (enabled = true);


--
-- Name: idx_task_control_event_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_control_event_created_at ON public.task_control_event USING btree (created_at);


--
-- Name: idx_task_control_event_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_control_event_status ON public.task_control_event USING btree (status);


--
-- Name: idx_task_control_event_task_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_control_event_task_id ON public.task_control_event USING btree (task_id);


--
-- Name: idx_tool_invocation_agent_run_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tool_invocation_agent_run_id ON public.tool_invocation USING btree (agent_run_id);


--
-- Name: llm_invocation_agent_run_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX llm_invocation_agent_run_idx ON public.llm_invocation USING btree (agent_run_id);


--
-- Name: llm_invocation_request_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX llm_invocation_request_id_idx ON public.llm_invocation USING btree (request_id);


--
-- Name: llm_invocation_task_id_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX llm_invocation_task_id_idx ON public.llm_invocation USING btree (task_id, id);


--
-- Name: llm_provider_sort_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX llm_provider_sort_idx ON public.llm_provider USING btree (sort_order, code);


--
-- Name: message_conversation_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX message_conversation_idx ON public.message USING btree (conversation_id, seq);


--
-- Name: proxy_traffic_host_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX proxy_traffic_host_idx ON public.proxy_traffic USING btree (host, captured_at DESC);


--
-- Name: proxy_traffic_path_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX proxy_traffic_path_idx ON public.proxy_traffic USING btree (host, path);


--
-- Name: task_assignment_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_assignment_idx ON public.task USING btree (assignment_id);


--
-- Name: task_heartbeat_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_heartbeat_idx ON public.task USING btree (heartbeat_at) WHERE (status = 'active'::text);


--
-- Name: task_host_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX task_host_idx ON public.task USING btree (target_host) WHERE (target_host <> ''::text);


--
-- Name: tool_invocation_name_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tool_invocation_name_idx ON public.tool_invocation USING btree (tool_name, created_at DESC);


--
-- Name: tool_invocation_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tool_invocation_task_idx ON public.tool_invocation USING btree (task_id, created_at DESC);


--
-- Name: tool_kind_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX tool_kind_idx ON public.tool USING btree (kind, sort_order);


--
-- Name: traffic_task_task_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX traffic_task_task_idx ON public.traffic_task USING btree (task_id, created_at);


--
-- Name: exploration_node trg_expl_node_increment_version; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER trg_expl_node_increment_version BEFORE UPDATE ON public.exploration_node FOR EACH ROW EXECUTE FUNCTION public.increment_exploration_node_version();


--
-- Name: exploration_node trg_expl_node_updated_at; Type: TRIGGER; Schema: public; Owner: -
--

CREATE TRIGGER trg_expl_node_updated_at BEFORE UPDATE ON public.exploration_node FOR EACH ROW EXECUTE FUNCTION public.update_exploration_node_updated_at();


--
-- Name: account_session account_session_account_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.account_session
    ADD CONSTRAINT account_session_account_id_fkey FOREIGN KEY (account_id) REFERENCES public.account(id) ON DELETE CASCADE;


--
-- Name: agent_run agent_run_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_run
    ADD CONSTRAINT agent_run_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE CASCADE;


--
-- Name: agent_traffic agent_traffic_agent_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_traffic
    ADD CONSTRAINT agent_traffic_agent_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_run(id) ON DELETE SET NULL;


--
-- Name: agent_traffic agent_traffic_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_traffic
    ADD CONSTRAINT agent_traffic_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE CASCADE;


--
-- Name: assignment assignment_schedule_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.assignment
    ADD CONSTRAINT assignment_schedule_id_fkey FOREIGN KEY (schedule_id) REFERENCES public.cron_schedule(id) ON DELETE SET NULL;


--
-- Name: conversation conversation_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.conversation
    ADD CONSTRAINT conversation_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE SET NULL;


--
-- Name: finding finding_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.finding
    ADD CONSTRAINT finding_agent_run_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_run(id) ON DELETE SET NULL;


--
-- Name: finding finding_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.finding
    ADD CONSTRAINT finding_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE CASCADE;


--
-- Name: exploration_edge fk_exploration_edge_dst; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.exploration_edge
    ADD CONSTRAINT fk_exploration_edge_dst FOREIGN KEY (dst_id) REFERENCES public.exploration_node(id) ON DELETE CASCADE;


--
-- Name: exploration_edge fk_exploration_edge_src; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.exploration_edge
    ADD CONSTRAINT fk_exploration_edge_src FOREIGN KEY (src_id) REFERENCES public.exploration_node(id) ON DELETE CASCADE;


--
-- Name: llm_invocation llm_invocation_agent_run_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_invocation
    ADD CONSTRAINT llm_invocation_agent_run_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_run(id) ON DELETE SET NULL;


--
-- Name: llm_invocation llm_invocation_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_invocation
    ADD CONSTRAINT llm_invocation_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE SET NULL;


--
-- Name: llm_role_route llm_role_route_provider_code_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.llm_role_route
    ADD CONSTRAINT llm_role_route_provider_code_fkey FOREIGN KEY (provider_code) REFERENCES public.llm_provider(code) ON DELETE RESTRICT;


--
-- Name: message message_conversation_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.message
    ADD CONSTRAINT message_conversation_id_fkey FOREIGN KEY (conversation_id) REFERENCES public.conversation(id) ON DELETE CASCADE;


--
-- Name: task task_assignment_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task
    ADD CONSTRAINT task_assignment_id_fkey FOREIGN KEY (assignment_id) REFERENCES public.assignment(id) ON DELETE CASCADE;


--
-- Name: tool_invocation tool_invocation_agent_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_invocation
    ADD CONSTRAINT tool_invocation_agent_task_id_fkey FOREIGN KEY (agent_run_id) REFERENCES public.agent_run(id) ON DELETE CASCADE;


--
-- Name: tool_invocation tool_invocation_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.tool_invocation
    ADD CONSTRAINT tool_invocation_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE CASCADE;


--
-- Name: traffic_task traffic_task_task_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_task
    ADD CONSTRAINT traffic_task_task_id_fkey FOREIGN KEY (task_id) REFERENCES public.task(id) ON DELETE CASCADE;


--
-- Name: traffic_task traffic_task_traffic_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.traffic_task
    ADD CONSTRAINT traffic_task_traffic_id_fkey FOREIGN KEY (traffic_id) REFERENCES public.proxy_traffic(id) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

-- PostgreSQL database dump complete
