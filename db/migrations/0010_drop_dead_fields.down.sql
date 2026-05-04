-- 0010 down: 恢复 6 列形状（不回填数据）。
ALTER TABLE agent_task ADD COLUMN budget jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE finding
    ADD COLUMN tool       text NOT NULL DEFAULT '',
    ADD COLUMN payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE engagement ADD COLUMN last_activity_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE http_flow
    ADD COLUMN request_truncated  boolean NOT NULL DEFAULT false,
    ADD COLUMN response_truncated boolean NOT NULL DEFAULT false;
