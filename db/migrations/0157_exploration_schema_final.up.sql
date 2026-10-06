-- 探索图表族 schema 定稿（详见提交说明）：
--   1. 冗余清除：roadmap 功能已从代码删除（表 + 列 + 索引陪葬）；
--      owner/tags/fingerprint/completed_at/com-conf 列从无读取方；
--      complexity/depends_on/blocked_reason/confidence/priority/source_type/source_id
--      为 metadata jsonb 的派生副本（读路径全部走 metadata），列删除后由 metadata 单源承载。
--   2. 冗余索引：idx_wm_node_metadata 与 idx_wm_node_metadata_gin 完全相同（留一）；
--      idx_wm_node_id_version / idx_wm_node_version 被 PK + CAS WHERE 覆盖。
--   3. 类型统一：探索图表族 + checkpoints + task_control_event 的 id/task_id
--      从 text 统一为 uuid（值一直就是 uuid 字符串，与 task/assignment 等表对齐）。
--   4. 索引名残留：idx_wm_*（world model 时代前缀，0148 只改了表名）→ idx_expl_*。
--   5. 规范：agent.complexity 补 NOT NULL；insight.tags 补 NOT NULL DEFAULT。

-- ── 1. roadmap 功能残留 ──────────────────────────────────────────────────────
DROP TABLE IF EXISTS exploration_roadmap_step;

-- ── 2. exploration_node 冗余列（唯一事实源 = content + metadata jsonb）───────
ALTER TABLE exploration_node
  DROP COLUMN IF EXISTS owner,
  DROP COLUMN IF EXISTS tags,
  DROP COLUMN IF EXISTS roadmap_step,
  DROP COLUMN IF EXISTS fingerprint,
  DROP COLUMN IF EXISTS completed_at,
  DROP COLUMN IF EXISTS complexity,
  DROP COLUMN IF EXISTS depends_on,
  DROP COLUMN IF EXISTS blocked_reason,
  DROP COLUMN IF EXISTS confidence,
  DROP COLUMN IF EXISTS priority,
  DROP COLUMN IF EXISTS source_type,
  DROP COLUMN IF EXISTS source_id;

-- 列删除自动连带其 CHECK/索引；显式清掉剩余的冗余/陪葬索引
DROP INDEX IF EXISTS idx_wm_node_fingerprint,
  idx_wm_node_fingerprint_pending,
  idx_wm_node_roadmap_step,
  idx_wm_node_tags,
  idx_wm_node_task_confidence,
  idx_wm_node_id_version,
  idx_wm_node_version,
  idx_wm_node_metadata;

-- ── 3. 类型统一 text → uuid ─────────────────────────────────────────────────
-- exploration_edge.src_id/dst_id 有 FK 指向 exploration_node.id：先摘约束，转换后重建
ALTER TABLE exploration_edge DROP CONSTRAINT IF EXISTS fk_exploration_edge_src;
ALTER TABLE exploration_edge DROP CONSTRAINT IF EXISTS fk_exploration_edge_dst;

ALTER TABLE exploration_node ALTER COLUMN id TYPE uuid USING id::uuid;
ALTER TABLE exploration_node ALTER COLUMN task_id TYPE uuid USING task_id::uuid;
ALTER TABLE exploration_edge ALTER COLUMN task_id TYPE uuid USING task_id::uuid;
ALTER TABLE exploration_edge ALTER COLUMN src_id TYPE uuid USING src_id::uuid;
ALTER TABLE exploration_edge ALTER COLUMN dst_id TYPE uuid USING dst_id::uuid;
ALTER TABLE exploration_verification ALTER COLUMN task_id TYPE uuid USING task_id::uuid;
ALTER TABLE exploration_verification ALTER COLUMN node_id TYPE uuid USING node_id::uuid;
ALTER TABLE checkpoints ALTER COLUMN task_id TYPE uuid USING task_id::uuid;
ALTER TABLE task_control_event ALTER COLUMN task_id TYPE uuid USING task_id::uuid;

ALTER TABLE exploration_edge
  ADD CONSTRAINT fk_exploration_edge_src FOREIGN KEY (src_id) REFERENCES exploration_node(id) ON DELETE CASCADE;
ALTER TABLE exploration_edge
  ADD CONSTRAINT fk_exploration_edge_dst FOREIGN KEY (dst_id) REFERENCES exploration_node(id) ON DELETE CASCADE;

-- ── 4. 索引名残留（world model 前缀）────────────────────────────────────────
ALTER INDEX IF EXISTS idx_wm_node_task_id RENAME TO idx_expl_node_task_id;
ALTER INDEX IF EXISTS idx_wm_node_task_kind RENAME TO idx_expl_node_task_kind;
ALTER INDEX IF EXISTS idx_wm_node_task_state RENAME TO idx_expl_node_task_state;
ALTER INDEX IF EXISTS idx_wm_node_metadata_gin RENAME TO idx_expl_node_metadata_gin;
ALTER INDEX IF EXISTS idx_wm_edge_dst RENAME TO idx_expl_edge_dst;
ALTER INDEX IF EXISTS idx_wm_edge_rel RENAME TO idx_expl_edge_rel;
ALTER INDEX IF EXISTS idx_wm_verification_outcome RENAME TO idx_expl_verification_outcome;
ALTER INDEX IF EXISTS idx_wm_verification_task_id RENAME TO idx_expl_verification_task_id;
ALTER INDEX IF EXISTS idx_exploration_verification_node_id RENAME TO idx_expl_verification_node_id;

-- ── 5. 规范补齐 ─────────────────────────────────────────────────────────────
ALTER TABLE agent ALTER COLUMN complexity SET NOT NULL;
ALTER TABLE insight ALTER COLUMN tags SET DEFAULT '{}';
UPDATE insight SET tags = '{}' WHERE tags IS NULL;
ALTER TABLE insight ALTER COLUMN tags SET NOT NULL;
