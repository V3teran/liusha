-- 0124: lead 包全面重构 - 三维分类系统
--
-- 设计决策：
-- 1. 三维分类：category（信息类型）× priority（优先级）× confidence（置信度）
-- 2. 删除旧的 kind 字段，替换为 category + priority + confidence
-- 3. detail 拆分为 summary（摘要）+ body（详细内容）
-- 4. 增加 tags 自由标签系统
-- 5. executor_id 重命名为 source_agent_id（语义更清晰）
-- 6. 8 种分类（简化版）
-- 7. 4 级优先级（扩展版）
--
-- 破坏性变更：与旧版本不兼容，需要清空数据或手动迁移

BEGIN;

-- 1. 删除旧表（如果存在）
DROP TABLE IF EXISTS lead CASCADE;

-- 2. 创建新表
CREATE TABLE lead (
    id TEXT PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id TEXT NOT NULL,

    -- 三维分类
    category TEXT NOT NULL DEFAULT 'note',
    priority TEXT NOT NULL DEFAULT 'medium',
    confidence TEXT NOT NULL DEFAULT 'possible',

    -- 内容
    summary TEXT NOT NULL,
    body TEXT,

    -- 元数据
    tags TEXT[],

    -- 追溯
    source_task_id TEXT NOT NULL,
    source_agent_id TEXT,

    -- 时间
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- 约束
    CONSTRAINT ck_lead_category CHECK (
        category IN (
            'target',        -- 目标信息
            'credential',    -- 凭证信息
            'infrastructure',-- 基础设施
            'business',      -- 业务逻辑
            'data',          -- 数据特征
            'finding',       -- 发现
            'obstacle',      -- 障碍
            'note'           -- 笔记
        )
    ),
    CONSTRAINT ck_lead_priority CHECK (
        priority IN ('critical', 'high', 'medium', 'low')
    ),
    CONSTRAINT ck_lead_confidence CHECK (
        confidence IN ('possible', 'probable', 'confirmed')
    ),
    CONSTRAINT ck_lead_summary_not_empty CHECK (
        length(trim(summary)) > 0
    )
);

-- 3. 创建索引
CREATE INDEX idx_lead_assignment_category ON lead(assignment_id, category);
CREATE INDEX idx_lead_assignment_priority ON lead(assignment_id, priority, created_at DESC);
CREATE INDEX idx_lead_assignment_confidence ON lead(assignment_id, confidence);
CREATE INDEX idx_lead_assignment_created ON lead(assignment_id, created_at DESC);
CREATE INDEX idx_lead_tags ON lead USING GIN(tags);

-- 4. 添加注释
COMMENT ON TABLE lead IS 'Assignment 级别情报黑板：跨 task 共享的轻量级观察记录';
COMMENT ON COLUMN lead.id IS '唯一标识';
COMMENT ON COLUMN lead.assignment_id IS '所属 assignment（隔离边界）';
COMMENT ON COLUMN lead.category IS '信息分类：target（目标）/credential（凭证）/infrastructure（基础设施）/business（业务逻辑）/data（数据）/finding（发现）/obstacle（障碍）/note（笔记）';
COMMENT ON COLUMN lead.priority IS '优先级：critical（关键，P0）/high（高，P1）/medium（中，P2）/low（低，P3）';
COMMENT ON COLUMN lead.confidence IS '置信度：possible（可能）/probable（很可能）/confirmed（已确认）';
COMMENT ON COLUMN lead.summary IS '一句话摘要（必填，200 字符以内）';
COMMENT ON COLUMN lead.body IS '详细内容（可选，markdown 格式）';
COMMENT ON COLUMN lead.tags IS '自由标签（可选）';
COMMENT ON COLUMN lead.source_task_id IS '产出该情报的 task.id（溯源）';
COMMENT ON COLUMN lead.source_agent_id IS '产出该情报的 agent（可选，溯源）';
COMMENT ON COLUMN lead.created_at IS '创建时间';
COMMENT ON COLUMN lead.updated_at IS '更新时间';

COMMIT;
