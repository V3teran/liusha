-- 0087: task.mode → scenario_id（删除主被动概念，改场景判别键）。
-- 见 docs/superpowers/plans/2026-08-01-scenario-playbook-architecture.md D2。
-- scenario_id 为裸 text（配置驱动，无 CHECK 无外键，应用层校验）。
-- 空库惯例：不搬存量，直接重置。

DELETE FROM task;  -- 空库前提，避免 NOT NULL 无默认值失败

-- 删依赖 mode 的索引
DROP INDEX IF EXISTS task_mode_status_idx;
DROP INDEX IF EXISTS task_heartbeat_idx;

-- 换列
ALTER TABLE task DROP COLUMN mode;
ALTER TABLE task ADD COLUMN scenario_id text NOT NULL;

-- 重建索引（heartbeat 去 mode 前导；status 索引以 scenario_id 前导）
CREATE INDEX task_scenario_status_idx ON task (scenario_id, status, created_at DESC);
CREATE INDEX task_heartbeat_idx ON task (heartbeat_at) WHERE status = 'active';

COMMENT ON TABLE task IS '统一扫描任务；scenario_id 标识所属场景（配置驱动，应用层校验）';
COMMENT ON COLUMN task.scenario_id IS '所属场景 id（如 web-pentest-killchain）；裸 text，无 DB 约束';
