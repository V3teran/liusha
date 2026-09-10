-- 给 wm_node 添加 roadmap_step 字段
--
-- 用途：
-- - 追踪 Action 是从哪个 RoadmapStep 派发的
-- - 统计某个 Step 派发了多少个 Action
-- - 判断 Step 是否完成（所有关联的 Action 都完成）

ALTER TABLE wm_node
ADD COLUMN IF NOT EXISTS roadmap_step REAL;

-- 索引：按 roadmap_step 查询 Action
CREATE INDEX IF NOT EXISTS idx_wm_node_roadmap_step ON wm_node(task_id, roadmap_step);

-- 注释
COMMENT ON COLUMN wm_node.roadmap_step IS '关联的 RoadmapStep 编号（如果此 Action 由 Roadmap 派发）';
