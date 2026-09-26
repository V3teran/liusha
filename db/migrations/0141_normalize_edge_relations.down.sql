ALTER TABLE wm_edge DROP CONSTRAINT ck_wm_edge_rel;
ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel
    CHECK (rel = ANY (ARRAY['GENERATES'::text, 'CONFIRMS'::text, 'REFUTES'::text, 'ENABLES'::text,
                            'DEPENDS_ON'::text, 'CONTRIBUTES'::text, 'INVALIDATES'::text, 'TRIGGERS'::text,
                            'derives'::text, 'enables'::text, 'on'::text]));
