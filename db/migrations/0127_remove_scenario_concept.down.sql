-- 回滚 0127：恢复 scenario 概念（不应该执行，scenario 已废弃）

-- 警告：此回滚脚本仅用于紧急情况，scenario 概念已完全废弃
-- 如果执行回滚，需要手动重新导入 scenario 数据

CREATE TABLE scenario (
    id text PRIMARY KEY,
    code text UNIQUE NOT NULL,
    name text NOT NULL,
    description text,
    instruction text,
    engine text NOT NULL CHECK (engine IN ('solo', 'swarm')),
    solo_executor_id text,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE task ADD COLUMN scenario_id text NOT NULL DEFAULT 'default';
ALTER TABLE assignment ADD COLUMN scenario_id text NOT NULL DEFAULT 'default';
ALTER TABLE cron_schedule ADD COLUMN scenario_id text NOT NULL DEFAULT 'default';

CREATE INDEX task_scenario_status_idx ON task (scenario_id, status, created_at DESC);
