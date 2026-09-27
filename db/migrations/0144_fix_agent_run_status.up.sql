-- 添加 status 列（如果不存在）
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='agent_run' AND column_name='status'
    ) THEN
        ALTER TABLE agent_run ADD COLUMN status text NOT NULL DEFAULT 'pending'
            CHECK (status IN ('pending','running','done','aborted','error'));
    END IF;
END $$;

-- 添加 result 列（如果不存在）
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='agent_run' AND column_name='result'
    ) THEN
        ALTER TABLE agent_run ADD COLUMN result jsonb NOT NULL DEFAULT '{}'::jsonb;
    END IF;
END $$;

-- 确保有 updated_at 列
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='agent_run' AND column_name='updated_at'
    ) THEN
        ALTER TABLE agent_run ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
    END IF;
END $$;

-- 添加 created_at 列（如果不存在）
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name='agent_run' AND column_name='created_at'
    ) THEN
        ALTER TABLE agent_run ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
    END IF;
END $$;

-- 添加索引以提升查询性能
CREATE INDEX IF NOT EXISTS idx_agent_run_status ON agent_run(status);
CREATE INDEX IF NOT EXISTS idx_agent_run_task_id ON agent_run(task_id);
