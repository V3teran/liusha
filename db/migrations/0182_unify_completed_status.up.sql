-- 终态成功词统一：done → completed（与 task / tool_invocation 等其余生命周期口径一致）。
-- 涉及两张活表：
--   agent_run.status（约束 agent_run_status_check，0017/0085/0144 演化而来）
--   exploration_node.state（0123 的 ck_wm_node_state，0148 的 DO 块已将其改名为
--   ck_exploration_node_state——约束名沿用该名字，仅替换值集）
--
-- 顺序必须是「先摘旧约束 → 再搬数据 → 后挂新约束」：CHECK 逐行校验，旧约束
-- 在场时写入 'completed'/'done' 的新值会被旧值集拒绝（非空表上必炸）。

-- agent_run
ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_status_check;
UPDATE agent_run SET status = 'completed' WHERE status = 'done';
ALTER TABLE agent_run ADD CONSTRAINT agent_run_status_check
    CHECK (status IN ('pending', 'running', 'completed', 'aborted', 'error'));

-- exploration_node
ALTER TABLE exploration_node DROP CONSTRAINT IF EXISTS ck_exploration_node_state;
UPDATE exploration_node SET state = 'completed' WHERE state = 'done';
ALTER TABLE exploration_node ADD CONSTRAINT ck_exploration_node_state
    CHECK (
        state IS NULL OR
        state IN ('open', 'blocked', 'running', 'completed', 'failed', 'exhausted', 'aborted')
    );
