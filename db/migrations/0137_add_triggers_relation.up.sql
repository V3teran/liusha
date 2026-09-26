-- Migration 0137: 添加 TRIGGERS 关系类型
--
-- 目的：支持持续探索循环中的 Result → Objective 触发关系
-- 语义：Result 触发新的探索目标（从发现中生成新的探索方向）
--
-- 这是持续探索模式的核心：每个 Result 都可以触发新的 Objective，
-- 形成 Objective → Action → Observation → Result → Objective 的闭环

-- 修改 wm_edge 表的 rel 约束，添加 TRIGGERS 关系
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_wm_edge_rel;

ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel CHECK (
    rel IN (
        -- Framework 标准关系（小写，对应 core.RelationKind）
        'GENERATES',    -- Objective → Action, Action → Observation
        'CONFIRMS',     -- Observation → Result
        'REFUTES',      -- Observation 反驳 Hypothesis
        'ENABLES',      -- Result → Action (发现使能新动作)
        'DEPENDS_ON',   -- Action → Action (依赖关系)
        'CONTRIBUTES',  -- Observation → Objective (贡献于目标)
        'INVALIDATES',  -- Observation → Action (使动作失效)
        'TRIGGERS',     -- Result → Objective (触发新探索目标) 【新增】
        -- 旧业务关系（兼容）
        'derives', 'enables', 'on'
    )
);

-- 更新注释
COMMENT ON CONSTRAINT ck_wm_edge_rel ON wm_edge IS
    'Framework 标准关系: GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON/CONTRIBUTES/INVALIDATES/TRIGGERS (持续探索循环)';
