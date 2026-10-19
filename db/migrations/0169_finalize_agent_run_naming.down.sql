-- 0169 down: 回滚 agent_run 命名和 insight.related_nodes

DO $$
BEGIN
    -- 1. 删除 insight.related_nodes 字段
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'insight' AND column_name = 'related_nodes'
    ) THEN
        ALTER TABLE insight DROP COLUMN related_nodes;
    END IF;

    -- 2. 将 agent_run 改回 agent_task（如果需要回滚）
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'agent_run'
    ) THEN
        ALTER TABLE agent_run RENAME TO agent_task;
    END IF;

    -- 3. 回滚索引名
    IF EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public' AND indexname = 'agent_run_engagement_idx'
    ) THEN
        ALTER INDEX agent_run_engagement_idx RENAME TO agent_task_engagement_idx;
    END IF;

END $$;
