-- 回滚 roadmap_step 字段
ALTER TABLE wm_node DROP COLUMN IF EXISTS roadmap_step;
