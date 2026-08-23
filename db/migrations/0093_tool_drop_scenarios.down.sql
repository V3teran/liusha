-- 0093 down: 恢复 tool.scenarios 列（回退用；reconcile 会重新写入）。
ALTER TABLE tool ADD COLUMN scenarios jsonb NOT NULL DEFAULT '[]'::jsonb;
COMMENT ON COLUMN tool.scenarios IS 'cli 工具的交战域标签（[web] 等）；function 工具恒为空数组';
