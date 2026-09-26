-- 关系命名统一到小写（5+2 语义）：
--   generates / confirms / refutes / enables / depends_on / belongs_to / triggers
-- 1) objective→action 的旧 enables 边改为 belongs_to（action→objective，方向归正）
UPDATE wm_edge e
SET rel = 'belongs_to', src_id = e.dst_id, dst_id = e.src_id
FROM wm_node s, wm_node d
WHERE e.src_id = s.id AND e.dst_id = d.id
  AND s.kind = 'objective' AND d.kind = 'action' AND e.rel = 'enables';

-- 2) 存量大写值统一转小写
UPDATE wm_edge SET rel = LOWER(rel) WHERE rel <> LOWER(rel);

-- 3) 清理白名单外的历史残留（derives / on 等旧模型值）
DELETE FROM wm_edge WHERE rel NOT IN
    ('generates', 'confirms', 'refutes', 'enables', 'depends_on', 'belongs_to', 'triggers');

-- 4) 收紧 CHECK 白名单
ALTER TABLE wm_edge DROP CONSTRAINT ck_wm_edge_rel;
ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel
    CHECK (rel = ANY (ARRAY['generates'::text, 'confirms'::text, 'refutes'::text, 'enables'::text,
                            'depends_on'::text, 'belongs_to'::text, 'triggers'::text]));
