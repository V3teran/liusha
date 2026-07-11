-- 0074: 多态 owner（owner_type + owner_id）→ 单列 task_id（P0 owner 坍缩）。
--
-- 设计（见 spec §9-B + §8.1b）：
--   - 合表后 owner_type 恒无意义（一切归属都是 task），双列多态退化为单外键。
--   - 4 张共享表 hunter / finding / llm_invocation / tool_invocation 去 owner_type+owner_id、
--     加 task_id uuid REFERENCES task(id)。合表后单外键天然成立，internal/owner 包删除。
--   - finding dedup：现状 UNIQUE(owner_id, dedup_key)（owner≈单次扫描级），owner_id 消失后
--     改 UNIQUE(task_id, dedup_key)，语义等价平移（单次扫描内去重）。dedup_key 生成列不变。
--
-- 存量可丢：旧行的 owner_id 不回填到 task_id（清库前提），直接换列。
-- 依赖列级 NOT NULL：新列先建再收紧（既有行会因无值违反 NOT NULL，故清库后跑）。

-- ── hunter（历经 agent_run→agent_task→hunter 重命名，owner 列自 0038 携带至今）
DROP INDEX IF EXISTS agent_run_owner_idx;
ALTER TABLE hunter DROP COLUMN IF EXISTS owner_type;
ALTER TABLE hunter DROP COLUMN IF EXISTS owner_id;
ALTER TABLE hunter ADD COLUMN task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE;
CREATE INDEX hunter_task_idx ON hunter (task_id, created_at DESC);

-- ── finding（dedup 键随 owner_id 消失而重建）
DROP INDEX IF EXISTS finding_owner_dedup_uniq;
DROP INDEX IF EXISTS finding_owner_idx;
ALTER TABLE finding DROP COLUMN IF EXISTS owner_type;
ALTER TABLE finding DROP COLUMN IF EXISTS owner_id;
ALTER TABLE finding ADD COLUMN task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE;
CREATE INDEX finding_task_idx ON finding (task_id, created_at DESC);
-- dedup_key 生成列（0048）保持不变，仅唯一约束的分组键 owner_id → task_id。
CREATE UNIQUE INDEX finding_task_dedup_uniq ON finding (task_id, dedup_key);

-- ── llm_invocation（owner 列本就 nullable，直接换）
DROP INDEX IF EXISTS llm_invocation_owner_idx;
ALTER TABLE llm_invocation DROP COLUMN IF EXISTS owner_type;
ALTER TABLE llm_invocation DROP COLUMN IF EXISTS owner_id;
ALTER TABLE llm_invocation ADD COLUMN task_id uuid REFERENCES task(id) ON DELETE SET NULL;
CREATE INDEX llm_invocation_task_idx ON llm_invocation (task_id);

-- ── tool_invocation
-- tool_invocation_task_idx 这个名字被 0046 占用过（当时建在 agent_task_id 上）；
-- 0055 把该列 rename 成 hunter_id 时索引未随之改名，需先清掉旧索引再建同名新索引。
DROP INDEX IF EXISTS tool_invocation_owner_idx;
DROP INDEX IF EXISTS tool_invocation_task_idx;
ALTER TABLE tool_invocation DROP COLUMN IF EXISTS owner_type;
ALTER TABLE tool_invocation DROP COLUMN IF EXISTS owner_id;
ALTER TABLE tool_invocation ADD COLUMN task_id uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE;
CREATE INDEX tool_invocation_task_idx ON tool_invocation (task_id, created_at DESC);

COMMENT ON COLUMN finding.dedup_key IS 'host+summary 前 60 字（lower）拼接；UNIQUE(task_id, dedup_key) 单次扫描内去重';
