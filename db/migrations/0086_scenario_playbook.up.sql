-- 0086: 建 4 张配置表 hunter / playbook / playbook_hunter / scenario（DB 事实源）。
-- DDL 逐字取自 plan D1；建表顺序满足 FK 依赖：hunter、playbook → playbook_hunter → scenario。
-- 前置：M0 的 0085 已把旧运行记录表 hunter 腾名为 hunter_run，此处 hunter 是全新配置表。

-- ① hunter：离散领域猎手（原子，可被任意 playbook 组合）
CREATE TABLE hunter (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code           text        NOT NULL UNIQUE,
    kind           text        NOT NULL CHECK (kind IN ('orchestrator','domain')),
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    body           text        NOT NULL DEFAULT '',
    tools          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    max_iterations int         NOT NULL DEFAULT 40,
    enabled        boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- ② playbook：可复用的猎手组合
CREATE TABLE playbook (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text        NOT NULL UNIQUE,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    enabled      boolean     NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- ③ playbook_hunter：组合关系（多对多 + 顺序）
CREATE TABLE playbook_hunter (
    playbook_id  uuid  NOT NULL REFERENCES playbook(id) ON DELETE CASCADE,
    hunter_id    uuid  NOT NULL REFERENCES hunter(id)   ON DELETE RESTRICT,
    position     int   NOT NULL DEFAULT 0,
    PRIMARY KEY (playbook_id, hunter_id)
);
CREATE INDEX playbook_hunter_playbook_idx ON playbook_hunter (playbook_id, position);

-- ④ scenario：场景（引用一个 playbook + 独立选 engine）
CREATE TABLE scenario (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    code         text        NOT NULL UNIQUE,
    name         text        NOT NULL,
    description  text        NOT NULL DEFAULT '',
    instruction  text        NOT NULL DEFAULT '',
    domain       text        NOT NULL DEFAULT 'web',
    engine       text        NOT NULL CHECK (engine IN ('solo','swarm')),
    playbook_id  uuid        NOT NULL REFERENCES playbook(id) ON DELETE RESTRICT,
    enabled      boolean     NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX scenario_playbook_idx ON scenario (playbook_id);
