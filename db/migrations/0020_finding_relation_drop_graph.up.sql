-- 0020: finding_relation 表上线 + 砍掉 graph_node / graph_edge
--
-- 背景：之前的 graph 子系统试图把 endpoint/parameter/finding 拓扑全部存到
-- graph_node + graph_edge 两张表。深度复审发现：
--   - endpoint / parameter 都能从 finding.target / http_flow 投影派生，无独占信息
--   - finding 节点本身就是 finding 表的副本，靠 LLM 自填 dedup_key 还踩了
--     "同 finding 11 行" 的坑（normalizeFindingDedupKey 补丁就是这次留下的）
--   - 唯一独占价值是 finding ↔ finding 的 enables 边（组合漏洞推理）
-- 结论：砍掉整个 graph 子系统，保留唯一独占价值——新建 finding_relation 单表，
--      只存 enables 类边；endpoint/parameter/finding 节点全部由查询时投影器派生。
--
-- 净效果：-300 行代码（含 normalizeFindingDedupKey 补丁根治），-2 表 +1 表。

-- ===== 1. 砍掉 graph_node / graph_edge =====
DROP TABLE IF EXISTS graph_edge;
DROP TABLE IF EXISTS graph_node;

-- ===== 2. 新建 finding_relation =====
-- (from_finding_id, to_finding_id, kind) UNIQUE 幂等：主 ReAct 跨轮重复推理同一组合
-- 不会重复落库。kind 当前只 'enables'（A 是 B 的前提），未来按需扩展。
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
