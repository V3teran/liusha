-- 0029: 重命名 engagement.memory_notes 列为 notes
--
-- 背景：v1.1 重设计把 "memory" 概念统一为 "note"（短期记忆 vs lesson 长期记忆）。
-- write_memory / read_memory 工具改为 write_note / read_note；DB 列名同步收敛。
--
-- 数据语义不变（仍是 {"notes": [...]} jsonb），仅列名重命名。
-- 用户要求"不留兼容代码"，直接 RENAME COLUMN，旧引用一律失败。

ALTER TABLE engagement RENAME COLUMN memory_notes TO notes;
