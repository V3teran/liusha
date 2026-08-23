-- 0097: LLM 配置迁 DB——provider 部署注册表 + 命名别名 + 角色路由。
-- 取代 config.yaml 的 providers:/llm.agents/llm.default_provider 等静态映射：DB 成事实源，
-- 前端「模型模块」CRUD 改这些表，api/runner 经多级缓存运行期热读（改一处、跨进程即时生效）。
--
-- 沿用业界（LiteLLM/OpenRouter/Portkey）两层结构：
--   · llm_provider  = 部署注册表（连接参数 + 能力标志）；一个 base_url+model+key 一条
--   · llm_alias     = 命名别名（default/light/vision/fallback，可扩），别名 → provider
--   · llm_role_route= 角色 → 别名（planner→vision 等）；role 缺省走 'default' 别名
-- 消费方按 role 解析：role → 别名 → provider。别名层让「换一个视觉模型」改一处即全量跟随。
--
-- 关键约束：api_key_env 只存**环境变量名**（如 GLM_API_KEY），密钥值永不落库（见安全规范）。
-- retry/invocation 等启动期一次性装配的参数不迁——仍留 yaml（非热改、无跨进程一致性需求）。

CREATE TABLE llm_provider (
    key             text        NOT NULL PRIMARY KEY,          -- 稳定引用键（别名/审计聚合按此）
    type            text        NOT NULL CHECK (type IN ('openai_compat','anthropic')),
    base_url        text        NOT NULL,
    default_model   text        NOT NULL,
    api_key_env     text        NOT NULL,                      -- 仅 ENV 变量名，绝不存密钥值
    max_tokens      int         NOT NULL DEFAULT 4096,
    supports_tools  boolean     NOT NULL DEFAULT true,
    supports_vision boolean     NOT NULL,                      -- 强制显式声明（对齐 yaml validate）
    context_window  int         NOT NULL CHECK (context_window > 0), -- model 总上下文窗口 tokens
    description     text        NOT NULL DEFAULT '',
    sort_order      int         NOT NULL DEFAULT 0,
    enabled         boolean     NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX llm_provider_sort_idx ON llm_provider (sort_order, key);

COMMENT ON TABLE llm_provider IS 'LLM provider 部署注册表（取代 config.yaml providers:）；DB 为事实源，运行期多级缓存热读';
COMMENT ON COLUMN llm_provider.api_key_env IS '环境变量名（如 GLM_API_KEY）；密钥值永不落库';

CREATE TABLE llm_alias (
    name         text        NOT NULL PRIMARY KEY,             -- default/light/vision/fallback（可扩）
    provider_key text        NOT NULL REFERENCES llm_provider(key) ON DELETE RESTRICT,
    description  text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE llm_alias IS 'LLM 命名别名 → provider；别名层让换模型改一处即全量跟随（default 别名兼作缺省路由）';

CREATE TABLE llm_role_route (
    role       text        NOT NULL PRIMARY KEY,               -- traffic-analysis/planner/exploitation/inspector/compactor…
    alias      text        NOT NULL REFERENCES llm_alias(name) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE llm_role_route IS '角色 → 别名路由；role 未命中走 default 别名（与旧 default_provider 语义一致）';
