-- 0008 down: 把三列加回（仅 schema 形状还原，不回填数据）。
ALTER TABLE engagement
    ADD COLUMN memory_facts jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN memory_ideas jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN memory_hints jsonb NOT NULL DEFAULT '{}'::jsonb;
