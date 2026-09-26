ALTER TABLE wm_verification ADD CONSTRAINT fk_wm_verification_node
    FOREIGN KEY (node_id) REFERENCES wm_node(id) ON DELETE CASCADE;
ALTER TABLE wm_node DROP CONSTRAINT ck_wm_node_source_type;
ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_source_type
    CHECK (source_type = ANY (ARRAY['user'::text, 'planner'::text, 'executor'::text, 'verifier'::text, 'system'::text]));
