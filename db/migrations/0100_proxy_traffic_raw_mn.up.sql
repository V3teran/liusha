-- 0100: proxy_traffic 改 raw 报文单一存储 + traffic↔task 多对多。
--
-- 动机（见流量重构 spec）：
--   1) 详情页要 Burp 式「整条 HTTP 报文文本」（请求行/状态行 + 头 + body 一体），
--      拆成 headers(jsonb)+body(bytea) 四列既要前端重新拼接、又丢了报文原貌（头顺序/大小写/
--      重复头/原始 request-line）。改存 request_raw/response_raw 单一 canonical 源，
--      展示直接吐、replay 从 raw 反解（http.ReadRequest），无冗余（用户明确选 C：raw 单源、不重复）。
--   2) 一条捕获流量可被多个 passive task 复用（按 host 攒批 + 显式下发都可能命中同一条），
--      consumed_by_task_id 单列只能记「最后一个」。改 traffic_task 关联表表达真实 M:N。
--
--   content_type/http_version 从报文抽出的检索/装配信号（http_version 用于忠实重建 request-line）。
--
-- 存量：raw 无法由 SQL 从 jsonb+bytea 忠实回拼，旧行 request_raw/response_raw 留 NULL
--   （0075 已立「存量可丢」惯例，e2e 清库重跑）；consumed_by_task_id 先回填进 traffic_task 再删列。

-- ── 1) M:N 关联表（先建，供回填）
CREATE TABLE traffic_task (
    traffic_id  bigint NOT NULL REFERENCES proxy_traffic(id) ON DELETE CASCADE,
    task_id     uuid   NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (traffic_id, task_id)
);
-- 反向查（某 task 消费了哪些流量）
CREATE INDEX traffic_task_task_idx ON traffic_task (task_id, created_at);

COMMENT ON TABLE traffic_task IS '代理流量↔passive task 多对多消费关系（一条捕获可被多 task 复用）';

-- ── 2) 回填：把旧 consumed_by_task_id 单列迁进关联表
INSERT INTO traffic_task (traffic_id, task_id, created_at)
SELECT id, consumed_by_task_id, captured_at
FROM proxy_traffic
WHERE consumed_by_task_id IS NOT NULL;

-- ── 3) 删旧单列消费指针 + 其部分索引
DROP INDEX IF EXISTS proxy_traffic_unconsumed_idx;
ALTER TABLE proxy_traffic DROP COLUMN consumed_by_task_id;

-- ── 4) 拆列 → raw 单源
ALTER TABLE proxy_traffic
    DROP COLUMN request_headers,
    DROP COLUMN request_body,
    DROP COLUMN response_headers,
    DROP COLUMN response_body,
    ADD  COLUMN request_raw  bytea,
    ADD  COLUMN response_raw bytea,
    ADD  COLUMN content_type text NOT NULL DEFAULT '',
    ADD  COLUMN http_version text NOT NULL DEFAULT '';

COMMENT ON COLUMN proxy_traffic.request_raw  IS '完整请求报文文本（request-line + 头 + body），展示/replay 单一源';
COMMENT ON COLUMN proxy_traffic.response_raw IS '完整响应报文文本（status-line + 头 + body），body 已解压/截断';
COMMENT ON COLUMN proxy_traffic.content_type IS '响应 Content-Type 主类型（前端 Pretty 美化判定）';
COMMENT ON COLUMN proxy_traffic.http_version IS '协议版本 token（HTTP/1.1 等），忠实重建 request/status line';
