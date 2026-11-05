-- 0195 down: 删 task.paused 标记列。

ALTER TABLE task DROP COLUMN IF EXISTS paused;
