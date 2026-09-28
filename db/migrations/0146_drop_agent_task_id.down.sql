-- 反向不可精确重建（历史 task 关联已丢失）；仅恢复列形态供旧代码兼容。
ALTER TABLE agent ADD COLUMN IF NOT EXISTS task_id uuid REFERENCES task(id) ON DELETE CASCADE;
