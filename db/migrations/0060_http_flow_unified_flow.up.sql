-- 0060: http_flow 扩展为统一流量字典——外部（passive 入口）+ 内部（active agent 工具）
--
-- 背景：
--   v1.x 阶段 http_flow 仅服务 passive 流量（外部代理捕获 → ingestor → traffic-analysis）。
--   v2 阶段 active 模式 agent 工具流量（curl/python/browser_use）也走 liusha proxy
--   （cmd/proxy 双端口 8888=external/8889=internal），同表存储统一流量字典，
--   planner/exploitation 通过 list_flows/view_flow/replay_flow 工具复用历史流量。
--
-- 字段变更：
--   + owner_type  text NOT NULL CHECK (passive_session|active_scan)
--   + owner_id    uuid NOT NULL              -- 多态 owner（替代 passive_session_id 单值）
--   + source      text NOT NULL CHECK (external|internal) DEFAULT 'external'
--   + agent_id   uuid NULL FK→agent(id)    -- 仅 internal 填（哪个 agent 发的）
--   + duration_ms int  NOT NULL DEFAULT 0
--   + path        text NOT NULL              -- 从 url 抽出（glob 查询索引用）
--   - passive_session_id                     -- 被 owner_type+owner_id 完全替代，删除
--
-- 索引变更：
--   - http_flow_host_idx (passive_session_id, host)  -- 老索引随字段删自动消失
--   + http_flow_owner_host_idx (owner_id, host, created_at DESC)
--   + http_flow_agent_idx    (agent_id) WHERE agent_id IS NOT NULL  -- partial
--   + http_flow_source_idx    (source, created_at DESC)
--   + http_flow_path_idx      (host, path)  -- glob 查询

-- ── 1) 添加新字段（先 nullable，回填后 SET NOT NULL）
ALTER TABLE http_flow ADD COLUMN owner_type text;
ALTER TABLE http_flow ADD COLUMN owner_id uuid;
ALTER TABLE http_flow ADD COLUMN source text NOT NULL DEFAULT 'external'
    CHECK (source IN ('external', 'internal'));
ALTER TABLE http_flow ADD COLUMN agent_id uuid REFERENCES agent(id) ON DELETE SET NULL;
ALTER TABLE http_flow ADD COLUMN duration_ms int NOT NULL DEFAULT 0;
ALTER TABLE http_flow ADD COLUMN path text;

-- ── 2) 回填 owner（现有数据全 passive）
UPDATE http_flow
   SET owner_type = 'passive_session',
       owner_id   = passive_session_id;

-- ── 3) 回填 path（从 url 抽出）
-- 正则：跳过 'scheme://host' 部分，捕获到 '?' 之前
-- 边界：无 path 或 url 异常 fallback 到 '/'
UPDATE http_flow
   SET path = COALESCE(NULLIF(substring(url FROM '://[^/]+(/[^?]*)'), ''), '/');

-- ── 4) 收紧 NOT NULL（回填完毕保证非空）
ALTER TABLE http_flow ALTER COLUMN owner_type SET NOT NULL;
ALTER TABLE http_flow ALTER COLUMN owner_id   SET NOT NULL;
ALTER TABLE http_flow ALTER COLUMN path       SET NOT NULL;
ALTER TABLE http_flow ADD CONSTRAINT http_flow_owner_type_check
    CHECK (owner_type IN ('passive_session', 'active_scan'));

-- ── 5) 删老字段（不留兼容）
ALTER TABLE http_flow DROP COLUMN passive_session_id;

-- ── 6) 新索引
CREATE INDEX http_flow_owner_host_idx ON http_flow (owner_id, host, created_at DESC);
CREATE INDEX http_flow_agent_idx     ON http_flow (agent_id) WHERE agent_id IS NOT NULL;
CREATE INDEX http_flow_source_idx     ON http_flow (source, created_at DESC);
CREATE INDEX http_flow_path_idx       ON http_flow (host, path);
