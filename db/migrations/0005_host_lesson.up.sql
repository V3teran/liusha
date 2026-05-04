-- 0005: host_lesson 跨 engagement 长期知识库（P2）
--
-- 背景：v1.2 已加 engagement.memory_hints 跨 task 复用 + Rotator top-20 hint 继承，
-- 但 proxy 模式 engagement 滚动后超出 top-20 的 hint 永久丢失；整站模式重复扫同站
-- 也无法继承上次经验。
--
-- 本表：distill 蒸馏的"目标级长期经验"，per (tenant, host)，跨 engagement 永久持久化。
-- 与 engagement.memory_hints 双写：
--   - engagement.memory_hints = 单 engagement 内 scratch / write_hint 工具目标
--   - host_lesson           = 高质量蒸馏知识，下次扫同 host 自动加载
--
-- 失效策略：当前不实现 TTL；hit_count 字段为未来 LRU eviction / verifier 校验留接口。

CREATE TABLE host_lesson (
    id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            text        NOT NULL DEFAULT 'default',
    host                 text        NOT NULL CHECK (host <> ''),
    content              text        NOT NULL CHECK (content <> ''),
    -- content_hash：SHA-256 hex 字符串（64 字符），用于 (tenant, host, content) 去重，
    -- 避免精确字符串比较开销 + 索引大小爆炸（content 可达 200 字符）。
    content_hash         text        NOT NULL,
    priority             int         NOT NULL DEFAULT 5 CHECK (priority BETWEEN 1 AND 10),
    source_engagement_id uuid        REFERENCES engagement(id) ON DELETE SET NULL,
    source_finding_id    uuid        REFERENCES finding(id)    ON DELETE SET NULL,
    hit_count            int         NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- 同 (tenant, host) 同 content 去重：distill 反复对类似 finding 蒸馏出相同结论时合并 hit_count
CREATE UNIQUE INDEX host_lesson_dedup ON host_lesson (tenant_id, host, content_hash);

-- 读侧排序索引：loadHintsForPrompt 按 (host, priority desc, updated_at desc) top-N 拉取
CREATE INDEX host_lesson_lookup ON host_lesson (tenant_id, host, priority DESC, updated_at DESC);
