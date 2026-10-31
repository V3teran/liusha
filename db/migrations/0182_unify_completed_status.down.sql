-- 回滚：completed → done（仅还原 0182 涉及的两张表）。

ALTER TABLE agent_run DROP CONSTRAINT IF EXISTS agent_run_status_check;
UPDATE agent_run SET status = 'done' WHERE status = 'completed';
ALTER TABLE agent_run ADD CONSTRAINT agent_run_status_check
    CHECK (status IN ('pending', 'running', 'done', 'aborted', 'error'));

ALTER TABLE exploration_node DROP CONSTRAINT IF EXISTS ck_exploration_node_state;
UPDATE exploration_node SET state = 'done' WHERE state = 'completed';
ALTER TABLE exploration_node ADD CONSTRAINT ck_exploration_node_state
    CHECK (
        state IS NULL OR
        state IN ('open', 'blocked', 'running', 'done', 'failed', 'exhausted', 'aborted')
    );
