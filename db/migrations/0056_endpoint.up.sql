-- endpoint 表：active 模式攻击面注册表。
--
-- 设计动机：planner recon 识别的功能模块（endpoint）此前只活在 write_note 自由文本里，
-- graph 投影源仅 finding，导致 active 模式 graph 只显示有 finding 的 endpoint，
-- recon 识别但未挖出 finding 的模块完全消失。
-- 本表把 planner recon 沉淀为结构化 fact，graph projector 双源融合：
--   - active 模式：从本表读 endpoint
--   - passive 模式：仍从 finding 反推（http_flow 流水账不写本表，零冗余）
--
-- 跟 http_flow 区别（不冗余）：
--   - http_flow: 每次请求 1 行（流水账，含 body，仅 passive 写入）
--   - endpoint:  每个独立 endpoint 1 行（dedup，无 body，仅 active 写入）
--
-- 状态机（2 值）：
--   - 'discovered': planner recon 识别（write_endpoint 创建）
--   - 'tested_vulnerable': exploitation write_finding 后自动联动（按 host+method+path 匹配）
-- tested_clean / spawned 状态 YAGNI 暂不加（planner 可用 list_exploitations + read_endpoints 自决）

CREATE TABLE endpoint (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        uuid        NOT NULL,
    host            text        NOT NULL,
    method          text        NOT NULL,
    path            text        NOT NULL,  -- 模板化（复用 graphview.templatizePath：/user/1 → /user/:id）
    status          text        NOT NULL DEFAULT 'discovered',
    discovered_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_id, host, method, path),
    CONSTRAINT endpoint_status_check CHECK (status IN ('discovered', 'tested_vulnerable'))
);

-- 范围查询索引：planner 查"本 owner+host 的所有 endpoint"
CREATE INDEX endpoint_owner_host_idx ON endpoint(owner_id, host);

-- 状态过滤索引：planner done 前自检查"未挖出的 endpoint"
CREATE INDEX endpoint_owner_status_idx ON endpoint(owner_id, status);
