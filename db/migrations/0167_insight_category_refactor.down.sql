-- 回滚 insight category 重构

BEGIN;

-- 1. 回滚数据（逆向映射）
UPDATE insight SET category = 'target' WHERE category = 'entity';
-- 注意：无法区分原来的 target 和 data，统一回滚为 target
UPDATE insight SET category = 'credential' WHERE category = 'access';
UPDATE insight SET category = 'infrastructure' WHERE category = 'structure';
UPDATE insight SET category = 'business' WHERE category = 'behavior';
UPDATE insight SET category = 'obstacle' WHERE category = 'issue';
-- 注意：无法区分原来的 anomaly 和 obstacle，统一回滚为 obstacle
-- note 保持不变

-- 2. 恢复旧约束
ALTER TABLE insight DROP CONSTRAINT IF EXISTS ck_insight_category;
ALTER TABLE insight ADD CONSTRAINT ck_insight_category
  CHECK (category IN ('target', 'credential', 'infrastructure', 'business', 'data', 'obstacle', 'note'));

-- 3. 删除 severity 约束和字段
ALTER TABLE insight DROP CONSTRAINT IF EXISTS ck_insight_severity;
ALTER TABLE insight DROP COLUMN severity;

-- 4. 恢复注释
COMMENT ON COLUMN insight.category IS '信息分类';

COMMIT;
