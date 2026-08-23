-- 0102 down: 删对外顺序号 seq（连带 UNIQUE 约束与 bigserial 隐式序列）。

ALTER TABLE finding DROP CONSTRAINT IF EXISTS finding_seq_unique;
ALTER TABLE finding DROP COLUMN seq;
