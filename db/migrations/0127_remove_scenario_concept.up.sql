-- 0127: 完全删除 scenario 概念
-- scenario 概念已废弃，不再需要场景配置，全部删除

-- 1. 删除依赖 scenario_id 的索引
DROP INDEX IF EXISTS task_scenario_status_idx;

-- 2. 删除外键列
ALTER TABLE task DROP COLUMN IF EXISTS scenario_id;
ALTER TABLE assignment DROP COLUMN IF EXISTS scenario_id;
ALTER TABLE cron_schedule DROP COLUMN IF EXISTS scenario_id;

-- 3. 删除 scenario 表
DROP TABLE IF EXISTS scenario CASCADE;

-- 4. 更新表注释
COMMENT ON TABLE task IS '统一扫描任务，不再区分场景';
COMMENT ON TABLE assignment IS '下发容器，所有任务的根';
