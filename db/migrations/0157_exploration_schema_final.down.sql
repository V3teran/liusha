-- 回滚 0157：仅恢复结构骨架，不恢复已删数据（冗余列本无读取方）
ALTER TABLE insight ALTER COLUMN tags DROP NOT NULL;
ALTER TABLE insight ALTER COLUMN tags DROP DEFAULT;
ALTER TABLE agent ALTER COLUMN complexity DROP NOT NULL;

ALTER INDEX IF EXISTS idx_expl_node_task_id RENAME TO idx_wm_node_task_id;
ALTER INDEX IF EXISTS idx_expl_node_task_kind RENAME TO idx_wm_node_task_kind;
ALTER INDEX IF EXISTS idx_expl_node_task_state RENAME TO idx_wm_node_task_state;
ALTER INDEX IF EXISTS idx_expl_node_metadata_gin RENAME TO idx_wm_node_metadata_gin;
ALTER INDEX IF EXISTS idx_expl_edge_dst RENAME TO idx_wm_edge_dst;
ALTER INDEX IF EXISTS idx_expl_edge_rel RENAME TO idx_wm_edge_rel;
ALTER INDEX IF EXISTS idx_expl_verification_outcome RENAME TO idx_wm_verification_outcome;
ALTER INDEX IF EXISTS idx_expl_verification_task_id RENAME TO idx_wm_verification_task_id;
ALTER INDEX IF EXISTS idx_expl_verification_node_id RENAME TO idx_exploration_verification_node_id;

ALTER TABLE exploration_edge DROP CONSTRAINT IF EXISTS fk_exploration_edge_src;
ALTER TABLE exploration_edge DROP CONSTRAINT IF EXISTS fk_exploration_edge_dst;

ALTER TABLE task_control_event ALTER COLUMN task_id TYPE text USING task_id::text;
ALTER TABLE checkpoints ALTER COLUMN task_id TYPE text USING task_id::text;
ALTER TABLE exploration_verification ALTER COLUMN node_id TYPE text USING node_id::text;
ALTER TABLE exploration_verification ALTER COLUMN task_id TYPE text USING task_id::text;
ALTER TABLE exploration_edge ALTER COLUMN src_id TYPE text USING src_id::text;
ALTER TABLE exploration_edge ALTER COLUMN dst_id TYPE text USING dst_id::text;
ALTER TABLE exploration_edge ALTER COLUMN task_id TYPE text USING task_id::text;
ALTER TABLE exploration_node ALTER COLUMN task_id TYPE text USING task_id::text;
ALTER TABLE exploration_node ALTER COLUMN id TYPE text USING id::text;

ALTER TABLE exploration_edge
  ADD CONSTRAINT fk_exploration_edge_src FOREIGN KEY (src_id) REFERENCES exploration_node(id) ON DELETE CASCADE;
ALTER TABLE exploration_edge
  ADD CONSTRAINT fk_exploration_edge_dst FOREIGN KEY (dst_id) REFERENCES exploration_node(id) ON DELETE CASCADE;

ALTER TABLE exploration_node
  ADD COLUMN IF NOT EXISTS owner text,
  ADD COLUMN IF NOT EXISTS tags text[] DEFAULT '{}'::text[],
  ADD COLUMN IF NOT EXISTS roadmap_step real,
  ADD COLUMN IF NOT EXISTS fingerprint text,
  ADD COLUMN IF NOT EXISTS completed_at timestamptz,
  ADD COLUMN IF NOT EXISTS complexity text,
  ADD COLUMN IF NOT EXISTS depends_on text[] DEFAULT '{}'::text[],
  ADD COLUMN IF NOT EXISTS blocked_reason text,
  ADD COLUMN IF NOT EXISTS confidence text,
  ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 50,
  ADD COLUMN IF NOT EXISTS source_type text NOT NULL DEFAULT 'system',
  ADD COLUMN IF NOT EXISTS source_id text NOT NULL DEFAULT 'framework';
