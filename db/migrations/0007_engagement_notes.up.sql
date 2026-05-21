-- 0007: engagement 加 memory_notes 列（合并旧 memory_facts + memory_ideas）
--
-- 背景：v1.2 收尾把 facts/ideas 合并成 notes，单一 take_note 工具写入。
-- 原 memory_facts/memory_ideas 列保留兼容存量；代码不再读写。
--
-- 新结构：
--   engagement.memory_notes jsonb = {"notes": [
--     {"kind": "observation"|"hypothesis"|"boundary",
--      "content": "...", "status": "...", "task_id": "...", "scope": "engagement"}
--   ]}

ALTER TABLE engagement ADD COLUMN IF NOT EXISTS memory_notes jsonb NOT NULL DEFAULT '{}'::jsonb;
