-- 探索图与复现审计链挂上任务外键，并按实际查询整理索引。
--
-- 1. 外键：任务下的其它数据（运行、发现、流量、调用记录……）都以外键挂在 task 上，探索图节点与
--    审计链是仅有的例外——库里不保证它们指向存在的任务与节点。补上外键，与其它表同一口径
--    （任务删除时随之删除）。先清掉不指向任何任务 / 节点的孤儿行：它们在任何界面与查询里都不可达。
-- 2. 审计链主键是 Go 侧生成的 UUID，列却是 text：改为 uuid，与其它主键同类型。
-- 3. 索引按查询形态取舍：
--    - 边只按任务整体读取（主键前缀 task_id），另有按结果节点找生成它的假设（dst_id）；
--      外键检查按 src_id / dst_id 单列查找——补这两个单列索引，删掉没有查询使用的
--      (task_id, rel) 与 (task_id, dst_id)。
--    - 节点上 (task_id) 被 (task_id, kind) 的前缀覆盖；content / metadata 两个 GIN 索引没有任何
--      运行期查询使用（只有包含 / 存在运算能用 GIN，运行期查询都是 ->> 取值比较）。
--    - (task_id, kind, state) 的部分索引谓词含不存在的状态 blocked（状态约束里没有它），重建。
--    - 审计链上 (task_id, outcome) 没有查询使用（按任务读取另有 task_id 索引）。
-- 4. 评估者曾在假设节点上写验证次数、失败原因、沿用来源、重复归属四个键，没有任何读取方，
--    同样的信息记在审计链里（界面展示的也是审计链）。清掉存量，不再写入。清理不是对节点的
--    业务修改，暂停版本与更新时间触发器，避免改动节点版本（规划依据）。

DELETE FROM exploration_verification v
WHERE NOT EXISTS (SELECT 1 FROM task t WHERE t.id = v.task_id)
   OR NOT EXISTS (SELECT 1 FROM exploration_node n WHERE n.id = v.node_id);

DELETE FROM exploration_node n
WHERE NOT EXISTS (SELECT 1 FROM task t WHERE t.id = n.task_id);

ALTER TABLE exploration_node
    ADD CONSTRAINT exploration_node_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES task(id) ON DELETE CASCADE;

-- 主键没有被任何地方引用：万一有不是 UUID 形式的历史值，换一个新 UUID。
ALTER TABLE exploration_verification
    ALTER COLUMN id TYPE uuid USING (CASE
        WHEN id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN id::uuid
        ELSE gen_random_uuid() END),
    ADD CONSTRAINT exploration_verification_task_id_fkey
        FOREIGN KEY (task_id) REFERENCES task(id) ON DELETE CASCADE,
    ADD CONSTRAINT exploration_verification_node_id_fkey
        FOREIGN KEY (node_id) REFERENCES exploration_node(id) ON DELETE CASCADE;

DROP INDEX IF EXISTS idx_expl_edge_rel;
DROP INDEX IF EXISTS idx_expl_edge_dst;
CREATE INDEX exploration_edge_src_idx ON exploration_edge (src_id);
CREATE INDEX exploration_edge_dst_idx ON exploration_edge (dst_id);

DROP INDEX IF EXISTS idx_expl_node_task_id;
DROP INDEX IF EXISTS idx_expl_node_content_gin;
DROP INDEX IF EXISTS idx_expl_node_metadata_gin;
DROP INDEX IF EXISTS idx_expl_node_task_kind_state;
CREATE INDEX idx_expl_node_task_kind_state ON exploration_node (task_id, kind, state)
    WHERE state IN ('open', 'running');

DROP INDEX IF EXISTS idx_expl_verification_outcome;

ALTER TABLE exploration_node DISABLE TRIGGER trg_expl_node_increment_version;
ALTER TABLE exploration_node DISABLE TRIGGER trg_expl_node_updated_at;
UPDATE exploration_node
SET metadata = metadata - ARRAY['failure_reason', 'verify_attempts', 'verdict_source', 'duplicate_of']
WHERE kind = 'observation'
  AND metadata ?| ARRAY['failure_reason', 'verify_attempts', 'verdict_source', 'duplicate_of'];
ALTER TABLE exploration_node ENABLE TRIGGER trg_expl_node_increment_version;
ALTER TABLE exploration_node ENABLE TRIGGER trg_expl_node_updated_at;
