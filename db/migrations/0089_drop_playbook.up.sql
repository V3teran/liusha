-- 0089: 删除剧本层，agent 编排改由 LLM 运行时自主。
--   · swarm：planner + 全部 enabled 领域 agent 作子代理池（运行时动态 handoff）
--   · solo：scenario.solo_agent_id 显式指向唯一 agent（无并集、无合体）
-- 同步：外置 CLI 工具下沉到 agent（cli_tools），与内置 tools 分列。
-- 空库惯例：不搬存量。

-- ① 丢弃剧本相关索引 + 表（先解除 scenario 对 playbook 的 FK）
DROP INDEX IF EXISTS scenario_playbook_idx;
ALTER TABLE scenario DROP COLUMN playbook_id;

DROP TABLE IF EXISTS playbook_agent;
DROP TABLE IF EXISTS playbook;

-- ② scenario 新增 solo 专用的单 agent 引用；solo 必填、swarm 必空
ALTER TABLE scenario ADD COLUMN solo_agent_id uuid REFERENCES agent(id) ON DELETE RESTRICT;
ALTER TABLE scenario ADD CONSTRAINT scenario_solo_agent_ck
    CHECK ((engine = 'solo') = (solo_agent_id IS NOT NULL));
CREATE INDEX scenario_solo_agent_idx ON scenario (solo_agent_id);

-- ③ agent 新增外置 CLI 工具白名单（独立于内置 tools）；空 = 域内全部可见
ALTER TABLE agent ADD COLUMN cli_tools jsonb NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN scenario.solo_agent_id IS 'solo 引擎唯一执行 agent；swarm 场景为 NULL';
COMMENT ON COLUMN agent.cli_tools IS '外置 CLI 工具白名单（tools.yaml 名字），空数组=域内全部可见';
