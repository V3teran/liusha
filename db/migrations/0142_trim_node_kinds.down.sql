ALTER TABLE wm_node DROP CONSTRAINT ck_wm_node_kind;
ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_kind
    CHECK ((kind = ANY (ARRAY['objective'::text, 'action'::text, 'observation'::text, 'evaluation'::text, 'result'::text, 'hypothesis'::text, 'evidence'::text, 'finding'::text, 'target'::text, 'asset'::text, 'credential'::text, 'access'::text])));
