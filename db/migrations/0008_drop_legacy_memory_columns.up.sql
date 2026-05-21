-- 0008: 删 engagement 表的三个 v1.2 前遗留 jsonb 列
--
-- 背景：v1.2 收尾把 facts/ideas 合并入 memory_notes（kind enum 区分 observation/
-- hypothesis/boundary）；旧 memory_hints 层因 host_lesson 表已是更优的长期知识层
-- 而废弃。0007 加 memory_notes 列后这三列就再无代码读写，留着只是技术债。
--
-- 兼容性：down migration 把列加回（jsonb 默认 '{}'）。生产存量数据已无价值，
-- 不做回填——历史 notes 已无法从 facts/ideas 还原（kind 信息丢失）。
ALTER TABLE engagement
    DROP COLUMN memory_facts,
    DROP COLUMN memory_ideas,
    DROP COLUMN memory_hints;
