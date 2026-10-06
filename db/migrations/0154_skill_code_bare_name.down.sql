-- 0154 down：尽力恢复（内置行拼回 <category>/<code> 前缀形式）。
-- 用户自建行（is_builtin=false）本就无前缀，不动。

UPDATE skill
SET code = category || '/' || code, updated_at = now()
WHERE is_builtin = true
  AND category <> ''
  AND code NOT LIKE '%/%'
  AND NOT EXISTS (
      SELECT 1 FROM skill t
      WHERE t.code = skill.category || '/' || skill.code
  );
