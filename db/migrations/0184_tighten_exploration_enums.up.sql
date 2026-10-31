-- 探索图词汇收紧：状态与边关系对齐代码的真实可达集。
--
-- 依据（以源码为准）：
--   1. state='blocked' 零写入方——依赖未满足的 action 保持 open，由 CanExecute
--      判定可执行性；独立状态需要配套的解锁流转，无消费者为其付费。
--   2. rel='depends_on' 零写入方——动作间依赖数据在节点 metadata.depends_on
--      （写入/读取经 planner/executor DAG 调度），从不落边。
--   3. exploration_edge.attrs 恒为列默认 '{}'：Go 侧无写入方、无读取方。
-- 三个值/列自引入起无存量语义（blocked 无行；depends_on 边无行；attrs 全默认），
-- 删除只约束未来，无需数据搬迁。

ALTER TABLE exploration_node DROP CONSTRAINT IF EXISTS ck_exploration_node_state;
ALTER TABLE exploration_node ADD CONSTRAINT ck_exploration_node_state
    CHECK (
        state IS NULL OR
        state IN ('open', 'running', 'completed', 'failed', 'exhausted', 'aborted')
    );

ALTER TABLE exploration_edge DROP CONSTRAINT IF EXISTS ck_exploration_edge_rel;
ALTER TABLE exploration_edge ADD CONSTRAINT ck_exploration_edge_rel
    CHECK (rel = ANY (ARRAY['generates'::text, 'triggers'::text]));

ALTER TABLE exploration_edge DROP COLUMN IF EXISTS attrs;
