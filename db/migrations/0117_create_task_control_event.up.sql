-- task_control_event: 人工干预事件表
-- 记录用户对运行中 Task 的控制指令（调整目标、注入 Move、暂停/恢复/终止）

CREATE TABLE IF NOT EXISTS task_control_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id TEXT NOT NULL,
    command TEXT NOT NULL, -- adjust_goal / inject_move / pause / resume / terminate
    payload JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processed', 'failed')),
    error TEXT
);

CREATE INDEX idx_task_control_event_task_id ON task_control_event(task_id);
CREATE INDEX idx_task_control_event_status ON task_control_event(status);
CREATE INDEX idx_task_control_event_created_at ON task_control_event(created_at);

COMMENT ON TABLE task_control_event IS '人工干预事件（控制平面 → Planner Agent）';
COMMENT ON COLUMN task_control_event.command IS '控制命令：adjust_goal（调整目标）、inject_move（注入 Move）、pause（暂停）、resume（恢复）、terminate（终止）';
COMMENT ON COLUMN task_control_event.payload IS '命令参数：adjust_goal{new_goal}, inject_move{move_kind,target_ref,reason,priority}';
COMMENT ON COLUMN task_control_event.status IS '处理状态：pending（待处理）、processed（已处理）、failed（处理失败）';
