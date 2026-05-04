-- 0014: 新增 flow_decision 表（落库 classify_traffic LLM 输出）
--
-- 借鉴 liusha2 decision 表设计：把"流量语义判断"（operation / resource_scope /
-- attack_surfaces / carries_auth / credential_locations / required_skills）显式落库，
-- 否则 classify_traffic 的 LLM 输出只活在 ReAct 内存里，归档后无法审计 / 复现 / 统计。
--
-- 与 liusha2 的差异：
--   - 显式 FK + 级联策略（engagement/flow CASCADE，task SET NULL，与 vuln_finding 一致）
--   - resource_scope 加 CHECK 约束并补 'unknown'（实测 LLM 会返这个值）
--   - 不带 llm_invocation_id：当前 instrument.Generate 不返回 invocation_id；
--     未来若需双向回查再加（YAGNI）
CREATE TABLE flow_decision (
    id                   bigserial PRIMARY KEY,
    engagement_id        uuid NOT NULL REFERENCES engagement(id) ON DELETE CASCADE,
    flow_id              bigint NOT NULL REFERENCES http_flow(id) ON DELETE CASCADE,
    task_id              uuid REFERENCES react_run(id) ON DELETE SET NULL,
    operation            text NOT NULL,
    resource_scope       text NOT NULL CHECK (resource_scope IN ('private','public','unknown')),
    attack_surfaces      jsonb NOT NULL DEFAULT '[]',
    carries_auth         boolean NOT NULL DEFAULT false,
    credential_locations jsonb NOT NULL DEFAULT '[]',
    required_skills      jsonb NOT NULL DEFAULT '[]',
    reasoning            text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX flow_decision_engagement_idx ON flow_decision(engagement_id, created_at);
CREATE INDEX flow_decision_flow_idx ON flow_decision(flow_id);
