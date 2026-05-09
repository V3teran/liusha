-- 0026: 重建 finding_relation 表
--
-- 0025 删过这张表（agentic 自由文本路线，用 summary 描述组合关系）；
-- v0024 反思后决定让 LLM 主动控图——加 write_relation 工具显式声明
-- finding A 是 finding B 的前提（enables 边），projector 渲染图边。

CREATE TABLE finding_relation (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    from_finding_id  uuid        NOT NULL REFERENCES finding(id) ON DELETE CASCADE,
    to_finding_id    uuid        NOT NULL REFERENCES finding(id) ON DELETE CASCADE,
    kind             text        NOT NULL,
    payload          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT finding_relation_kind_check CHECK (kind IN ('enables')),
    CONSTRAINT finding_relation_no_self CHECK (from_finding_id <> to_finding_id)
);

CREATE UNIQUE INDEX finding_relation_uniq
    ON finding_relation (from_finding_id, to_finding_id, kind);
CREATE INDEX finding_relation_from ON finding_relation (from_finding_id);
CREATE INDEX finding_relation_to   ON finding_relation (to_finding_id);
