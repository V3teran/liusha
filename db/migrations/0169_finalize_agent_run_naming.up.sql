-- 0169: 最终确定 agent_run 命名 + insight 添加 related_nodes
--
-- 背景：
--   0043 曾将 agent_run 改名为 agent_task（错误决策）
--   本迁移将其改回 agent_run（正确语义：运行实例，不是任务定义）
--
-- 设计理由：
--   1. agent_run 表存储的是运行实例（有状态、输入输出、时间），不是任务定义
--   2. 业界惯例：workflow run, pipeline run, task instance
--   3. agent_run 语义精确：一次具体的 agent 运行
--   4. insight 需要追溯到源节点（observation/result/action）

DO $$
BEGIN
    -- 1. 如果表名是 agent_task，改回 agent_run
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'agent_task'
    ) THEN
        ALTER TABLE agent_task RENAME TO agent_run;
        RAISE NOTICE 'Renamed table: agent_task -> agent_run';
    END IF;

    -- 2. 重命名相关索引（如果存在旧名）
    IF EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public' AND indexname = 'agent_task_engagement_idx'
    ) THEN
        ALTER INDEX agent_task_engagement_idx RENAME TO agent_run_engagement_idx;
        RAISE NOTICE 'Renamed index: agent_task_engagement_idx -> agent_run_engagement_idx';
    END IF;

    -- 3. insight 添加 related_nodes 字段（如果不存在）
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'insight' AND column_name = 'related_nodes'
    ) THEN
        ALTER TABLE insight ADD COLUMN related_nodes JSONB DEFAULT '[]'::jsonb;
        COMMENT ON COLUMN insight.related_nodes IS '关联的探索图节点 ID（observation/result/action）';
        RAISE NOTICE 'Added column: insight.related_nodes';
    END IF;

    -- 4. 更新注释
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = 'public' AND table_name = 'agent_run'
    ) THEN
        COMMENT ON TABLE agent_run IS 'Agent 运行实例（不是任务定义，是具体的一次运行）';
    END IF;

END $$;
