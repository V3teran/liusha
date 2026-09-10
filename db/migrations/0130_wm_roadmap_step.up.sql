-- 创建 Roadmap 表（Planner 的动态规划路线图）
--
-- 设计理念：
-- 1. Roadmap 是 Planner 的高层规划，记录探索式任务的执行路线
-- 2. 每个 RoadmapStep 是一个可验证的里程碑（中粒度，10-15 步）
-- 3. Planner 根据执行结果动态调整 Roadmap（完全替换式更新）
-- 4. Action 从 RoadmapStep 派生，1 Step → N Actions
--
-- 与 Action 的关系：
-- - RoadmapStep：高层目标（"测试 SQL 注入"）
-- - Action：低层执行（"测试 UNION 注入"、"测试布尔盲注"）
--
-- 状态系统：
-- - todo：待执行（Planner 已规划但未派发 Action）
-- - active：执行中（已派发 Action）
-- - complete：已完成（所有派生的 Action 都完成）
-- - skipped：已跳过（Planner 决定跳过此步骤）

CREATE TABLE IF NOT EXISTS wm_roadmap_step (
    task_id TEXT NOT NULL,
    step REAL NOT NULL,  -- 使用 REAL 支持插入小数步骤（如 1.5，动态插入）
    objective TEXT NOT NULL,  -- 步骤目标（如"识别 Web 服务类型并测试常见漏洞"）
    status TEXT NOT NULL DEFAULT 'todo',  -- todo/active/complete/skipped
    depends_on REAL[],  -- 依赖的步骤编号（如 [1.0, 2.0]）
    context JSONB,  -- 步骤的上下文数据（如发现的关键信息、中间结果）
    rationale TEXT,  -- Planner 为什么规划这一步（推理过程，用于调试）
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    PRIMARY KEY (task_id, step),

    -- 状态约束
    CONSTRAINT ck_roadmap_step_status CHECK (status IN ('todo', 'active', 'complete', 'skipped'))
);

-- 索引：按任务和状态查询
CREATE INDEX idx_wm_roadmap_step_status ON wm_roadmap_step(task_id, status);

-- 索引：按任务和步骤排序
CREATE INDEX idx_wm_roadmap_step_order ON wm_roadmap_step(task_id, step);

-- 注释
COMMENT ON TABLE wm_roadmap_step IS 'Planner 的动态 Roadmap（探索式规划路线图）';
COMMENT ON COLUMN wm_roadmap_step.step IS '步骤编号（支持小数，如 1.5 表示在 1 和 2 之间插入）';
COMMENT ON COLUMN wm_roadmap_step.objective IS '步骤目标（自然语言描述，如"测试 SQL 注入"）';
COMMENT ON COLUMN wm_roadmap_step.status IS '状态：todo（待执行）/active（执行中）/complete（已完成）/skipped（已跳过）';
COMMENT ON COLUMN wm_roadmap_step.depends_on IS '依赖的步骤编号（高层依赖，与 Action.depends_on 分层）';
COMMENT ON COLUMN wm_roadmap_step.context IS '步骤的上下文数据（如 {"discovered": "WordPress 5.8"}）';
COMMENT ON COLUMN wm_roadmap_step.rationale IS 'Planner 为什么规划这一步（推理过程，便于调试和审计）';
