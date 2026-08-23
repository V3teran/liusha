-- 0114: taskID → taskID 命名统一（世界模型隔离键修正）
--
-- 背景：taskID 是命名错误，应该是 taskID。世界模型以 Task 为隔离单位。
-- 三张表：wm_node, wm_edge, wm_verification 的 scan_id 列改名为 task_id。

-- ===== wm_node 表 =====

ALTER TABLE wm_node RENAME COLUMN scan_id TO task_id;

-- 更新唯一索引（包含 scan_id）
ALTER TABLE wm_node DROP CONSTRAINT wm_node_scan_id_kind_domain_ref_kind_locator_key;
ALTER TABLE wm_node ADD CONSTRAINT wm_node_task_id_kind_domain_ref_kind_locator_key
    UNIQUE (task_id, kind, domain, ref_kind, locator);

-- ===== wm_edge 表 =====

ALTER TABLE wm_edge RENAME COLUMN scan_id TO task_id;

-- 更新唯一索引（包含 scan_id）
ALTER TABLE wm_edge DROP CONSTRAINT wm_edge_scan_id_rel_src_dst_key;
ALTER TABLE wm_edge ADD CONSTRAINT wm_edge_task_id_rel_src_dst_key
    UNIQUE (task_id, rel, src, dst);

-- ===== wm_verification 表 =====

ALTER TABLE wm_verification RENAME COLUMN scan_id TO task_id;

-- ===== 注释更新 =====

COMMENT ON COLUMN wm_node.task_id IS '所属 Task ID（隔离键：不同 Task 的世界模型完全独立）';
COMMENT ON COLUMN wm_edge.task_id IS '所属 Task ID（隔离键）';
COMMENT ON COLUMN wm_verification.task_id IS '所属 Task ID（隔离键）';
