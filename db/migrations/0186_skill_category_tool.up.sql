-- skill.category 桶名对齐：tooling → tool。
--
-- 分类是面向人的二分知识域：vuln（漏洞知识）/ tool（工具使用知识）——两个裸
-- 域名词保持形态平行。tooling 在通用语义里指工具本体（build tooling），而桶内
-- 是 drive_browser 等能力的使用手册；tool 与 vuln 成对且与目录/种子口径一致。
-- 存量行只有种子写入的 'tooling'（browser-use），无用户自建数据依赖旧值。
UPDATE skill SET category = 'tool' WHERE category = 'tooling';

COMMENT ON COLUMN skill.category IS 'Skill 知识域：vuln（漏洞知识）或 tool（工具使用知识）';
