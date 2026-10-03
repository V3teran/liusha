-- 2026-10-02 全表字段审计（结合代码引用面逐列研判）：
--  1. engagement 表：0149 清理清单漏网——代码零引用、0 行。
--  2. agent_run.owner_type/owner_id：0074 多态 owner 坍缩残留，代码零引用。
--  3. agent_run.planner_id / parent_id：planner 子代理/子 run 树的老架构残留，代码零引用。
--  4. finding.source_flow_id：http_flow 拆分（0075）前的时代残留，代码零引用
--     （归属语义由 source_traffic_id 承载）。
--  5. finding.agent_id / tool_invocation.agent_id：列名与事实不符——FK 指向 agent_run
--     表却叫 agent_id；统一命名为 agent_run_id（与 llm_invocation.agent_run_id 对齐）。
--  6. 索引名 idx_wm_verification_node_id：0148 wm→exploration 改名漏网。

DROP TABLE IF EXISTS engagement;

ALTER TABLE agent_run DROP COLUMN IF EXISTS owner_type;
ALTER TABLE agent_run DROP COLUMN IF EXISTS owner_id;
ALTER TABLE agent_run DROP COLUMN IF EXISTS planner_id;
ALTER TABLE agent_run DROP COLUMN IF EXISTS parent_id;

ALTER TABLE finding DROP COLUMN IF EXISTS source_flow_id;
ALTER INDEX IF EXISTS idx_wm_verification_node_id RENAME TO idx_exploration_verification_node_id;

-- 列重命名（FK 约束随列自动跟随）
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='finding' AND column_name='agent_id')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='finding' AND column_name='agent_run_id') THEN
        ALTER TABLE finding RENAME COLUMN agent_id TO agent_run_id;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='tool_invocation' AND column_name='agent_id')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='tool_invocation' AND column_name='agent_run_id') THEN
        ALTER TABLE tool_invocation RENAME COLUMN agent_id TO agent_run_id;
    END IF;
END $$;
