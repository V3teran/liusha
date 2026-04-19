-- 0001: 建立 engagement 表（单次渗透测试的顶层生命周期实体）。
-- 同时启用 pgvector 扩展，后续迁移里的 embedding 字段会用到。

CREATE EXTENSION IF NOT EXISTS vector;

-- engagement: 一次渗透测试的总入口；三级生命周期 Engagement → AnalysisWindow → AgentTask 的顶层。
-- status 搭配 version 乐观锁，防止并发更新丢失。
CREATE TABLE engagement (
    id           uuid PRIMARY KEY,
    tenant_id    uuid NOT NULL,
    mode         text NOT NULL CHECK (mode IN ('proxy','browser','ctf')),
    scope        jsonb NOT NULL,
    status       text NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','paused','archived','aborted')),
    objective    text,
    version      bigint NOT NULL DEFAULT 1,
    started_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    ended_at     timestamptz,
    meta         jsonb
);

-- 租户维度按时间倒序的列表查询（控制台首页等）。
CREATE INDEX engagement_tenant_started_idx ON engagement (tenant_id, started_at DESC);
-- 运营视图只看活跃/暂停/终止态；已归档数据不入热索引，降低索引体积。
CREATE INDEX engagement_status_idx ON engagement (status) WHERE status <> 'archived';
