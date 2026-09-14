-- Migration 0135 down: 回滚 Framework GraphStore 数据库结构

-- 移除索引
DROP INDEX IF EXISTS idx_roadmap_task_step;
DROP INDEX IF EXISTS idx_wm_node_roadmap_step;
DROP INDEX IF EXISTS idx_wm_node_metadata_gin;

-- 恢复原有约束（如果需要）
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_wm_edge_rel;
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_state_by_kind;
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_confidence_fields;
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_kind;

-- 删除表
DROP TABLE IF EXISTS wm_roadmap_step;

-- 移除列
ALTER TABLE wm_node DROP COLUMN IF EXISTS roadmap_step;
ALTER TABLE wm_node DROP COLUMN IF EXISTS metadata;
