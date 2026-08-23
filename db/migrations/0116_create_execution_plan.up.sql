-- 0116: 创建 execution_plan 表（Move 独立存储）
--
-- 背景：Move 是"待执行的计划"，不应混入世界模型（已观察的状态）。
-- Planner Agent 通过 ProposeMoves 工具写入此表，Cognition Loop 从此表读取执行。

CREATE TABLE execution_plan (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id      text        NOT NULL,

    -- Move 类型（杀伤链阶段）
    kind         text        NOT NULL CHECK (kind IN ('enumerate', 'probe', 'exploit', 'escalate', 'persist')),

    -- 领域（4 域）
    domain       text        NOT NULL CHECK (domain IN ('web', 'binary', 'cloud', 'lateral')),

    -- 目标引用（三元组）
    target_ref   jsonb       NOT NULL,  -- {domain, ref_kind, locator}

    -- 优先级（数字越大优先级越高）
    priority     int         NOT NULL DEFAULT 5,

    -- 执行状态
    status       text        NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'executing', 'completed', 'failed')),

    -- 依赖关系（依赖的其他 Move IDs）
    depends_on   uuid[],

    -- Planner 规划理由
    reason       text        NOT NULL DEFAULT '',

    -- 时间戳
    created_at   timestamptz NOT NULL DEFAULT now(),
    started_at   timestamptz,
    completed_at timestamptz,

    -- 失败信息
    error_message text
);

-- 索引
CREATE INDEX execution_plan_task_id_status_idx ON execution_plan (task_id, status);
CREATE INDEX execution_plan_task_id_priority_idx ON execution_plan (task_id, priority DESC);

-- 注释
COMMENT ON TABLE execution_plan IS 'Move 执行计划表：Planner 产出的待执行动作，独立于世界模型存储';
COMMENT ON COLUMN execution_plan.kind IS '杀伤链阶段：enumerate（信息收集）| probe（漏洞探测）| exploit（漏洞利用）| escalate（权限提升）| persist（后渗透）';
COMMENT ON COLUMN execution_plan.domain IS '领域：web | binary | cloud | lateral';
COMMENT ON COLUMN execution_plan.target_ref IS '目标引用三元组 {domain, ref_kind, locator}，JSON 格式';
COMMENT ON COLUMN execution_plan.priority IS '优先级（数字越大越优先，默认 5）';
COMMENT ON COLUMN execution_plan.status IS 'pending（待执行）→ executing（执行中）→ completed/failed（完成/失败）';
COMMENT ON COLUMN execution_plan.depends_on IS '依赖的其他 Move IDs 数组（DAG 支持）';
COMMENT ON COLUMN execution_plan.reason IS 'Planner 规划理由（为什么产出这个 Move）';
