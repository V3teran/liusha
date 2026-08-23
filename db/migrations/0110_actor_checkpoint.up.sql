-- 0110: actor_checkpoint — ReAct 循环步骤快照，用于崩溃恢复。
-- 幂等键 (scan_id, move_id)：同一 Move 只保留最新快照，旧步不累积。
CREATE TABLE actor_checkpoint (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id     text NOT NULL,
    move_id     text NOT NULL,
    step_idx    int  NOT NULL,
    thought     text NOT NULL DEFAULT '',
    hypotheses  jsonb NOT NULL DEFAULT '[]',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (scan_id, move_id)
);
CREATE INDEX actor_checkpoint_scan_move_idx ON actor_checkpoint (scan_id, move_id);
