-- Move 入图：追踪每次执行的 Move 节点
CREATE TABLE wm_move (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scan_id     text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('enumerate', 'probe', 'exploit', 'escalate', 'persist')),
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'active', 'done', 'abandoned')),
    target_node uuid NOT NULL REFERENCES wm_node(id) ON DELETE CASCADE,
    reason      text NOT NULL DEFAULT '',
    outcome     jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX wm_move_scan_status_idx ON wm_move (scan_id, status);
CREATE INDEX wm_move_target_idx ON wm_move (target_node);

COMMENT ON TABLE wm_move IS '追踪规划器派发的每个 Move 执行节点';
COMMENT ON COLUMN wm_move.kind IS 'enumerate/probe/exploit/escalate/persist';
COMMENT ON COLUMN wm_move.status IS 'pending=待执行, active=执行中, done=完成, abandoned=放弃';
COMMENT ON COLUMN wm_move.target_node IS '在哪个世界模型节点上执行此 Move';
COMMENT ON COLUMN wm_move.reason IS 'Planner 给出的执行原因（展示给用户）';
COMMENT ON COLUMN wm_move.outcome IS 'Move 执行结果摘要（final_text/tool_calls/cognition）';

-- 扩充边类型：新增4种 Provenance 边
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS wm_edge_rel_check;
ALTER TABLE wm_edge ADD CONSTRAINT wm_edge_rel_check
    CHECK (rel IN ('derives', 'enables', 'on', 'spawns', 'produces', 'promotes', 'refutes'));

COMMENT ON CONSTRAINT wm_edge_rel_check ON wm_edge IS '7种边类型：原有3种(derives/enables/on) + 新增4种(spawns/produces/promotes/refutes)';
