-- 回滚 0186：恢复 tooling 桶名与原列注释。
UPDATE skill SET category = 'tooling' WHERE category = 'tool';

COMMENT ON COLUMN skill.category IS 'Skill 分类：tooling（工具类）或 vuln（漏洞类）';
