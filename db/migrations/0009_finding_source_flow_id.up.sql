-- 0009: finding 加 source_flow_id 列，建立 finding ↔ http_flow 1-FK 关联
--
-- 背景：v1.2 之前要回查"哪个流量触发了这个漏洞"，只能 jsonb 文本匹配
--   http_flow.url = finding.target->>'url'
-- 不准（query string 不同会漏）、无索引、不利于 SQL JOIN。
--
-- ON DELETE SET NULL：流量行被 GC 后 finding 仍保留（漏洞结论比原始流量更长寿）。
-- 存量 finding 行 source_flow_id 默认 NULL（无法回填，没有可靠的 url-only 匹配）。
ALTER TABLE finding
    ADD COLUMN source_flow_id bigint REFERENCES http_flow(id) ON DELETE SET NULL;

CREATE INDEX finding_source_flow_idx ON finding (source_flow_id) WHERE source_flow_id IS NOT NULL;
