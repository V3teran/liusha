-- 0022: lesson 表加 kind 列，区分 distill 蒸馏经验 vs 业务规则 hint
--
-- 背景：原 lesson 表只承载 distill 自动蒸馏的"目标级长期经验"。本次借鉴 Cairn
-- 的"业务规则数据化"思路（不抄实现）：把原本写死在 SKILL.md 里的硬规则
-- （如 BAC anonymous 优先级、合法 owner 剔除、length_ratio<0.5 反误判等）
-- 迁到本表 kind='hint' 行，让 LLM 在 user prompt 注入时看到，
-- 并支持运行时增删（不再绑死代码）。
--
-- host 段约定：
--   - kind='lesson' → 具体 host（distill 来自某次扫描），与原行为一致
--   - kind='hint'   → 既可以特定 host，也可以特殊值 '*' 表示对所有 host 通用业务规则
-- '*' 不破坏 host_check（仍非空），也不破坏 (tenant, host, content_hash) 唯一索引。
ALTER TABLE lesson ADD COLUMN kind text NOT NULL DEFAULT 'lesson'
  CHECK (kind IN ('lesson', 'hint'));

COMMENT ON COLUMN lesson.kind IS
  'lesson=distill 自动蒸馏的经验（per host）；hint=业务规则提醒（host 可为 "*" 表全局通用）。';

COMMENT ON COLUMN lesson.host IS
  '目标 host（含端口）；当 kind=hint 时特殊值 "*" 表示对所有 host 通用。';

-- 读侧索引补 kind 维度：LoadLessonsForPrompt 现在会按 (tenant, host_or_star, kind) 查
DROP INDEX IF EXISTS lesson_lookup;
CREATE INDEX lesson_lookup ON lesson (tenant_id, host, kind, priority DESC, updated_at DESC);
