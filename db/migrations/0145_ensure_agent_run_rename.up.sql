-- 确保 agent_task 被重命名为 agent_run（如果还没有的话）
DO $$
BEGIN
    -- 检查是否存在 agent_task 表
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'agent_task') THEN
        -- 如果 agent_run 表不存在，则重命名
        IF NOT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'agent_run' AND table_schema = 'public') THEN
            ALTER TABLE agent_task RENAME TO agent_run;
            RAISE NOTICE 'Renamed agent_task to agent_run';
        ELSE
            -- 如果两个表都存在，删除 agent_run 并重命名 agent_task
            DROP TABLE IF EXISTS agent_run CASCADE;
            ALTER TABLE agent_task RENAME TO agent_run;
            RAISE NOTICE 'Dropped old agent_run and renamed agent_task';
        END IF;
    END IF;
END $$;

-- 确保索引存在
CREATE INDEX IF NOT EXISTS idx_agent_run_status ON agent_run(status);
CREATE INDEX IF NOT EXISTS idx_agent_run_task_id ON agent_run(task_id);
CREATE INDEX IF NOT EXISTS idx_agent_run_created_at ON agent_run(created_at);
