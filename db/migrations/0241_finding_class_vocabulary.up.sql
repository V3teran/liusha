-- 发现类型收敛为封闭词表（internal/finding/class.go）：词表之外的类型（评估者没遵守场景的词表、
-- 自拟了类别）与空值归入 other。此后交付按同一词表规整，台账里不再有空类型与词表外的取值。
UPDATE finding
SET class = 'other'
WHERE class NOT IN (
  'reflected_xss', 'stored_xss', 'dom_xss', 'sql_injection', 'command_injection', 'ssti',
  'path_traversal', 'arbitrary_file_upload', 'ssrf', 'auth_bypass', 'access_control',
  'info_disclosure', 'security_misconfiguration', 'weak_credential', 'deserialization',
  'xxe', 'csrf', 'flag', 'other'
);

ALTER TABLE finding ALTER COLUMN class SET DEFAULT 'other';
COMMENT ON COLUMN finding.class IS '发现类型：封闭词表（internal/finding/class.go）之一，词表之外的与未给出的为 other';
