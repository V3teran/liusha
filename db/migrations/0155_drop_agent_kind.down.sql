-- 0155 down：恢复 kind 列（按 code 回填四角色值），重建 CHECK 与部分唯一索引。

ALTER TABLE agent ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'executor';
UPDATE agent SET kind = code WHERE code IN ('planner', 'executor', 'evaluator', 'monitor');

ALTER TABLE agent ADD CONSTRAINT agent_kind_check
    CHECK (kind = ANY (ARRAY['planner'::text, 'executor'::text, 'evaluator'::text, 'monitor'::text]));
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_kind_enabled ON agent (kind) WHERE enabled = true;
