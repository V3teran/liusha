-- 回滚：恢复历史宽松枚举（含四个从未生产的死值）。
ALTER TABLE exploration_edge DROP CONSTRAINT ck_exploration_edge_rel;
ALTER TABLE exploration_edge ADD CONSTRAINT ck_exploration_edge_rel
    CHECK (rel = ANY (ARRAY['generates'::text, 'confirms'::text, 'refutes'::text, 'enables'::text, 'depends_on'::text, 'belongs_to'::text, 'triggers'::text]));
