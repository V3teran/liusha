-- 终态成功词统一：done → completed（与 task / tool_invocation 等其余生命周期口径一致）。
-- 涉及两张活表：
--   agent_run.status（约束 agent_run_status_check，0017/0085/0144 演化而来）
--   exploration_node.state（0123 的 ck_wm_node_state，0148 的 DO 块已将其改名为
--   ck_exploration_node_state——约束名沿用该名字，仅替换值集）

-- agent_run：先搬数据再换约束
UPDATE agent_run SET status = 'completed' WHERE status = 'done';

ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_status_check;
ALTER TABLE agent_run ADD CONSTRAINT agent_run_status_check
    CHECK (status IN ('pending', 'running', 'completed', 'aborted', 'error'));

-- exploration_node：先搬数据再换约束
UPDATE exploration_node SET state = 'completed' WHERE state = 'done';

ALTER TABLE exploration_node DROP CONSTRAINT IF EXISTS ck_exploration_node_state;
ALTER TABLE exploration_node ADD CONSTRAINT ck_exploration_node_state
    CHECK (
        state IS NULL OR
        state IN ('open', 'blocked', 'running', 'completed', 'failed', 'exhausted', 'aborted')
    );
