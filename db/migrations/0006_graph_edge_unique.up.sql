-- 0006: 修复 graph_edge 的 ON CONFLICT 语义。
-- 背景：AddEdge 每次用 uuid.New() 生成新主键，导致 ON CONFLICT DO NOTHING 永不命中，
-- 同一语义关系 (engagement_id, src_id, dst_id, kind) 会被重复落盘。
-- 此处先清理历史重复数据，再补充 UNIQUE 约束，使应用层 ON CONFLICT 真正生效。

-- 兜底去重：保留 ctid 最大的一行（任意挑一条即可），删除剩余重复副本。
DELETE FROM graph_edge a USING graph_edge b
WHERE a.ctid < b.ctid
  AND a.engagement_id = b.engagement_id
  AND a.src_id = b.src_id
  AND a.dst_id = b.dst_id
  AND a.kind = b.kind;

-- 补充四元组 UNIQUE，保证语义去重。
ALTER TABLE graph_edge
    ADD CONSTRAINT graph_edge_unique_rel
    UNIQUE (engagement_id, src_id, dst_id, kind);
