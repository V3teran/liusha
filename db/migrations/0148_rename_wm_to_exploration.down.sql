DO $$
DECLARE
    r record;
    newname text;
BEGIN
    FOR r IN
        SELECT indexname FROM pg_indexes
        WHERE tablename IN ('exploration_node','exploration_edge','exploration_verification','exploration_roadmap_step')
          AND indexname LIKE 'exploration_%'
    LOOP
        newname := 'wm_' || substr(r.indexname, length('exploration_') + 1);
        EXECUTE format('ALTER INDEX %I RENAME TO %I', r.indexname, newname);
    END LOOP;

    FOR r IN
        SELECT conname, conrelid::regclass AS tbl FROM pg_constraint
        WHERE conrelid IN ('exploration_node'::regclass,'exploration_edge'::regclass,
                           'exploration_verification'::regclass,'exploration_roadmap_step'::regclass)
          AND conname LIKE 'exploration_%'
    LOOP
        newname := 'wm_' || substr(r.conname, length('exploration_') + 1);
        EXECUTE format('ALTER TABLE %I RENAME CONSTRAINT %I TO %I', r.tbl, r.conname, newname);
    END LOOP;
END $$;

ALTER TABLE exploration_roadmap_step RENAME TO wm_roadmap_step;
ALTER TABLE exploration_verification RENAME TO wm_verification;
ALTER TABLE exploration_edge RENAME TO wm_edge;
ALTER TABLE exploration_node RENAME TO wm_node;
