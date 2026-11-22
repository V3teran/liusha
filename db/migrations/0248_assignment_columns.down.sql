-- 回到删列之前（原值不恢复）。
ALTER TABLE assignment ADD COLUMN payload jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE assignment ADD COLUMN description text NOT NULL DEFAULT '';
