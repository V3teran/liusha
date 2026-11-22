-- 回到删列之前（删掉的列以缺省值恢复，原值不恢复）。
CREATE INDEX idx_agent_run_task_id ON agent_run (task_id);
ALTER TABLE agent_run ADD COLUMN result jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE agent_run ADD COLUMN role text NOT NULL DEFAULT 'planner'
    CONSTRAINT agent_run_role_check CHECK (role IN ('planner', 'executor', 'evaluator', 'monitor'));
