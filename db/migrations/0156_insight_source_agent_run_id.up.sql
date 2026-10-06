-- 统一"指向 agent_run 的引用列"命名为 *_agent_run_id（命名契约见 docs/glossary.md）：
--   - agent_id = agent 实体（配置角色）外键的保留名；agent_run_id = 执行实例外键。
--   - insight.source_agent_id 的值实为 run id（cognition 注入的 agentRunID），
--     列名却是"哪个 agent"——0124 自 executor_id 改名而来，本次定稿纠正。

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='insight' AND column_name='source_agent_id')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='insight' AND column_name='source_agent_run_id') THEN
        ALTER TABLE insight RENAME COLUMN source_agent_id TO source_agent_run_id;
        COMMENT ON COLUMN insight.source_agent_run_id IS '产出该情报的认知轮次 agent_run.id（溯源）';
    END IF;
END $$;
