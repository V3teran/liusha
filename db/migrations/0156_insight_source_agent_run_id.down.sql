-- 回滚 source_agent_run_id → source_agent_id（回到 0124 命名）
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='insight' AND column_name='source_agent_run_id')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='insight' AND column_name='source_agent_id') THEN
        ALTER TABLE insight RENAME COLUMN source_agent_run_id TO source_agent_id;
    END IF;
END $$;
