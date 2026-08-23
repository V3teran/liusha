-- 回滚 0114: taskID → taskID

-- wm_node 表
ALTER TABLE wm_node RENAME COLUMN task_id TO scan_id;
ALTER TABLE wm_node DROP CONSTRAINT wm_node_task_id_kind_domain_ref_kind_locator_key;
ALTER TABLE wm_node ADD CONSTRAINT wm_node_scan_id_kind_domain_ref_kind_locator_key
    UNIQUE (scan_id, kind, domain, ref_kind, locator);

-- wm_edge 表
ALTER TABLE wm_edge RENAME COLUMN task_id TO scan_id;
ALTER TABLE wm_edge DROP CONSTRAINT wm_edge_task_id_rel_src_dst_key;
ALTER TABLE wm_edge ADD CONSTRAINT wm_edge_scan_id_rel_src_dst_key
    UNIQUE (scan_id, rel, src, dst);

-- wm_verification 表
ALTER TABLE wm_verification RENAME COLUMN task_id TO scan_id;
