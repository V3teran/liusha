-- wm_* 表族更名 exploration_*：与包名 explorationgraph / 术语「探索图」对齐。
-- 旧前缀 wm（world model）是探索图概念定名前的残留。

ALTER TABLE wm_node RENAME TO exploration_node;
ALTER TABLE wm_edge RENAME TO exploration_edge;
ALTER TABLE wm_verification RENAME TO exploration_verification;
ALTER TABLE wm_roadmap_step RENAME TO exploration_roadmap_step;

-- 索引与约束同步改名（Postgres 表改名不自动跟随索引/约束名）
DO $$
DECLARE
    r record;
    newname text;
BEGIN
    FOR r IN
        SELECT indexname FROM pg_indexes
        WHERE tablename IN ('exploration_node','exploration_edge','exploration_verification','exploration_roadmap_step')
          AND indexname LIKE 'wm_%'
    LOOP
        newname := 'exploration_' || substr(r.indexname, 4);
        EXECUTE format('ALTER INDEX %I RENAME TO %I', r.indexname, newname);
    END LOOP;

    FOR r IN
        SELECT conname, conrelid::regclass AS tbl FROM pg_constraint
        WHERE conrelid IN ('exploration_node'::regclass,'exploration_edge'::regclass,
                           'exploration_verification'::regclass,'exploration_roadmap_step'::regclass)
          AND conname LIKE '%wm_%'
    LOOP
        newname := replace(r.conname, 'wm_', 'exploration_');
        EXECUTE format('ALTER TABLE %I RENAME CONSTRAINT %I TO %I', r.tbl, r.conname, newname);
    END LOOP;
END $$;
