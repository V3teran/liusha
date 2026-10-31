-- 回滚 0184：恢复 attrs 列与宽松枚举。
ALTER TABLE exploration_edge ADD COLUMN IF NOT EXISTS attrs JSONB DEFAULT '{}';

ALTER TABLE exploration_edge DROP CONSTRAINT IF EXISTS ck_exploration_edge_rel;
ALTER TABLE exploration_edge ADD CONSTRAINT ck_exploration_edge_rel
    CHECK (rel = ANY (ARRAY['generates'::text, 'depends_on'::text, 'triggers'::text]));

ALTER TABLE exploration_node DROP CONSTRAINT IF EXISTS ck_exploration_node_state;
ALTER TABLE exploration_node ADD CONSTRAINT ck_exploration_node_state
    CHECK (
        state IS NULL OR
        state IN ('open', 'blocked', 'running', 'completed', 'failed', 'exhausted', 'aborted')
    );
