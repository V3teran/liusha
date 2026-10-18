-- 重构 insight category：去渗透测试化，消除冗余
-- 变更：
--   target + data → entity（实体）
--   credential → access（访问）
--   infrastructure → structure（结构）
--   business → behavior（行为）
--   anomaly + obstacle → issue（问题）
--   note → note（保持）
-- 新增：severity 字段（仅 category=issue 时有意义）

BEGIN;

-- 1. 添加 severity 字段
ALTER TABLE insight ADD COLUMN severity TEXT;

-- 2. 先删除旧约束
ALTER TABLE insight DROP CONSTRAINT IF EXISTS ck_insight_category;

-- 3. 迁移现有数据（在无约束状态下）
UPDATE insight SET category = 'entity' WHERE category = 'target';
UPDATE insight SET category = 'entity' WHERE category = 'data';
UPDATE insight SET category = 'access' WHERE category = 'credential';
UPDATE insight SET category = 'structure' WHERE category = 'infrastructure';
UPDATE insight SET category = 'behavior' WHERE category = 'business';
UPDATE insight SET category = 'issue' WHERE category = 'obstacle';
-- note 保持不变

-- 4. 添加新约束
ALTER TABLE insight ADD CONSTRAINT ck_insight_category
  CHECK (category IN ('entity', 'access', 'structure', 'behavior', 'issue', 'note'));

-- 5. 添加 severity 约束
ALTER TABLE insight ADD CONSTRAINT ck_insight_severity
  CHECK (severity IS NULL OR severity IN ('low', 'medium', 'high', 'critical'));

-- 6. 添加注释
COMMENT ON COLUMN insight.category IS '信息分类：entity（实体）/access（访问）/structure（结构）/behavior（行为）/issue（问题）/note（备注）';
COMMENT ON COLUMN insight.severity IS '严重性（仅 category=issue 时有意义）：low/medium/high/critical';

COMMIT;
