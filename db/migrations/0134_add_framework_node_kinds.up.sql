-- Migration 0134: 添加 Framework 标准节点类型支持
--
-- 目的：支持 Framework（ADK）的标准认知循环节点类型
-- - objective: 任务目标（对应 ReAct Thought / PDDL Goal）
-- - action: 执行动作（对应 ReAct Action / PDDL Action）
-- - observation: 观察结果（对应 ReAct Observation / PDDL State）
-- - evaluation: 评估结论（质量评分、验证结果）
-- - result: 最终结果（已确认的发现）
--
-- 这些是 AI Agent 的通用认知模式，不是业务特定类型

-- 1. 修改 wm_node 表的 kind 约束，添加 Framework 标准类型
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_kind;

ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_kind CHECK (
    kind IN (
        -- Framework 标准类型（ADK 通用）
        'objective', 'action', 'observation', 'evaluation', 'result',
        -- 旧业务类型（兼容，逐步迁移）
        'hypothesis', 'evidence', 'finding',
        'target', 'asset', 'credential', 'access'
    )
);

-- 2. 修改 wm_node 表的约束，使 observation/evaluation/result 可以使用 confidence 字段
ALTER TABLE wm_node DROP CONSTRAINT IF EXISTS ck_wm_node_confidence_fields;

ALTER TABLE wm_node ADD CONSTRAINT ck_wm_node_confidence_fields CHECK (
    (kind IN ('hypothesis', 'finding', 'observation', 'evaluation', 'result') AND confidence IS NOT NULL)
    OR kind NOT IN ('hypothesis', 'finding', 'observation', 'evaluation', 'result')
);

-- 3. 修改 wm_edge 表的 rel 约束，添加 Framework 标准关系类型
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS ck_wm_edge_rel;

ALTER TABLE wm_edge ADD CONSTRAINT ck_wm_edge_rel CHECK (
    rel IN (
        -- Framework 标准关系（小写，对应 core.RelationKind）
        'GENERATES', 'CONFIRMS', 'REFUTES', 'ENABLES', 'DEPENDS_ON',
        'CONTRIBUTES', 'INVALIDATES',
        -- 旧业务关系（兼容）
        'derives', 'enables', 'on'
    )
);

-- 注释
COMMENT ON CONSTRAINT ck_wm_node_kind ON wm_node IS
    'Framework 标准类型: objective/action/observation/evaluation/result (ADK通用认知循环)';

COMMENT ON CONSTRAINT ck_wm_edge_rel ON wm_edge IS
    'Framework 标准关系: GENERATES/CONFIRMS/REFUTES/ENABLES/DEPENDS_ON/CONTRIBUTES/INVALIDATES';
