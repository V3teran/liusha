-- 回到外键与索引整理之前（清掉的孤儿行与元数据键不恢复）。
DROP INDEX IF EXISTS idx_expl_node_task_kind_state;
CREATE INDEX idx_expl_node_task_kind_state ON exploration_node (task_id, kind, state)
    WHERE state IN ('running', 'open', 'blocked');
CREATE INDEX idx_expl_node_task_id ON exploration_node (task_id);
CREATE INDEX idx_expl_node_content_gin ON exploration_node USING gin (content);
CREATE INDEX idx_expl_node_metadata_gin ON exploration_node USING gin (metadata);

DROP INDEX IF EXISTS exploration_edge_src_idx;
DROP INDEX IF EXISTS exploration_edge_dst_idx;
CREATE INDEX idx_expl_edge_rel ON exploration_edge (task_id, rel);
CREATE INDEX idx_expl_edge_dst ON exploration_edge (task_id, dst_id);

CREATE INDEX idx_expl_verification_outcome ON exploration_verification (task_id, outcome);

ALTER TABLE exploration_verification
    DROP CONSTRAINT exploration_verification_node_id_fkey,
    DROP CONSTRAINT exploration_verification_task_id_fkey,
    ALTER COLUMN id TYPE text USING id::text;

ALTER TABLE exploration_node DROP CONSTRAINT exploration_node_task_id_fkey;
