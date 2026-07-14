-- 0075: http_flow 拆两张表 —— proxy_traffic（代理捕获，属 host）+ agent_traffic（agent 自产，属 task）。
--
-- 设计（见 spec §5）：
--   现状 http_flow 靠 source 字段混装两种性质完全不同的流量：
--     external = 代理捕获的真实用户流量，被分析的输入，属于 host，先于任何 task
--     internal = agent 在 sandbox 自产的流量，干活副产物/弹药，属于 task，不触发分析
--   合表是「形状相同就合」的错误。拆分后归属天然清晰。
--
--   proxy_traffic：按 host 落，consumed_by_task_id 标记被哪个 passive task 消费（聚合时回填，可空）。
--   agent_traffic：按 task_id 落，保留 identity/tool 戳（0065 能力），是 replay/list/view flow 弹药 + sitemap 源。
--
-- 存量可丢：不搬运旧 http_flow 数据，直接建新表 + DROP 旧表（0077 或此处）。
-- body 大字段截断由 Go store 层各自保留（32KiB），与旧 flow.Store 一致。

-- ── proxy_traffic（代理捕获，属 host，先于 task）
CREATE TABLE proxy_traffic (
    id           bigserial PRIMARY KEY,
    host         text NOT NULL,               -- 归属轴（先于 task 存在）
    method       text NOT NULL,
    scheme       text NOT NULL DEFAULT '',
    url          text NOT NULL DEFAULT '',
    path         text NOT NULL DEFAULT '',    -- 从 url 抽出，glob 查询索引用
    status_code  int  NOT NULL DEFAULT 0,
    request_headers  jsonb,
    request_body     bytea,
    response_headers jsonb,
    response_body    bytea,
    duration_ms  int  NOT NULL DEFAULT 0,
    -- 被哪个 passive task 消费（聚合成 task 时回填；未消费为 NULL）
    consumed_by_task_id uuid REFERENCES task(id) ON DELETE SET NULL,
    captured_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX proxy_traffic_host_idx ON proxy_traffic (host, captured_at DESC);
CREATE INDEX proxy_traffic_unconsumed_idx ON proxy_traffic (host)
    WHERE consumed_by_task_id IS NULL;
CREATE INDEX proxy_traffic_path_idx ON proxy_traffic (host, path);

-- ── agent_traffic（agent 自产，属 task）
CREATE TABLE agent_traffic (
    id           bigserial PRIMARY KEY,
    task_id      uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    hunter_id    uuid REFERENCES hunter(id) ON DELETE SET NULL,  -- 哪个 agent 发的
    identity     text,   -- 身份戳（browser_use 的 identity / 登录账号）
    tool         text,   -- 工具戳（browser / curl / sqlmap ...）
    host         text NOT NULL,
    method       text NOT NULL,
    url          text NOT NULL DEFAULT '',
    path         text NOT NULL DEFAULT '',
    status_code  int  NOT NULL DEFAULT 0,
    request_headers  jsonb,
    request_body     bytea,
    response_headers jsonb,
    response_body    bytea,
    duration_ms  int  NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_traffic_task_idx ON agent_traffic (task_id, created_at);
CREATE INDEX agent_traffic_identity_tool_idx ON agent_traffic (task_id, identity, tool);
CREATE INDEX agent_traffic_path_idx ON agent_traffic (host, path);

-- ── finding.source_flow_id 去外键（拆表后跨表多态引用，单一 FK 无法表达）
-- 一条 finding 的来源流量：passive 来自 proxy_traffic、active 来自 agent_traffic，
-- 两表 id 各自独立。source_flow_id 只是审计指针（代码从不 JOIN，仅存取/回显），
-- 故降级为无外键的裸 bigint——与 owner_id 去 FK 同源判断（spec §5：跨表引用丢 FK）。
ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_source_flow_id_fkey;

-- ── 删旧统一流量表
DROP TABLE IF EXISTS http_flow;

COMMENT ON TABLE proxy_traffic IS '代理捕获的真实流量（被分析输入，属 host，先于 task）';
COMMENT ON TABLE agent_traffic IS 'agent 自产流量（干活副产物/弹药，属 task，不触发分析）';
COMMENT ON COLUMN proxy_traffic.consumed_by_task_id IS '被哪个 passive task 消费（聚合时回填；NULL=未消费）';
