-- 自 migrations/0112_clean_empty_thinking_metadata.sql 收编（原文件从未接入 golang-migrate）。
-- 数据规范：assistant 消息 thinking 为空时完全省略字段，而非存储空值/空对象——
-- 空值形态曾致前端折叠区渲染崩溃。幂等：重复执行无副作用。
UPDATE message
SET metadata = metadata - 'thinking'
WHERE kind = 'message'
  AND role = 'assistant'
  AND metadata ? 'thinking'
  AND (
    metadata->>'thinking' = ''
    OR metadata->'thinking' = '{}'
  );
