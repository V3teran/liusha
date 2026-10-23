-- Migration 0112: 清理 message.metadata 中的空 thinking 字段
--
-- 问题背景：旧版本 AppendAssistant 会将空字符串 thinking 序列化为 {"thinking":""}，
-- 甚至在某些边缘情况下产生 {"thinking":{}} 的脏数据，导致前端渲染崩溃。
--
-- 修复策略：
--   1. 找出 metadata->>'thinking' = '' 的记录（空字符串）
--   2. 找出 metadata->'thinking' = '{}' 的记录（空对象，罕见但存在）
--   3. 将这些记录的 metadata 中的 thinking 字段移除
--
-- 数据规范：thinking 为空时应完全省略该字段，而非存储空值。
-- 对应后端修复：cmd/api/adapters.go AppendAssistant 方法。

BEGIN;

-- 清理空字符串 thinking（最常见情况）
UPDATE message
SET metadata = metadata - 'thinking'
WHERE kind = 'message'
  AND role = 'assistant'
  AND metadata IS NOT NULL
  AND metadata ? 'thinking'
  AND (metadata->>'thinking' = '' OR metadata->>'thinking' IS NULL);

-- 清理空对象 thinking（罕见但会导致前端崩溃）
-- JSONB 类型判断：'{}' 等价于空对象
UPDATE message
SET metadata = metadata - 'thinking'
WHERE kind = 'message'
  AND role = 'assistant'
  AND metadata IS NOT NULL
  AND metadata ? 'thinking'
  AND metadata->'thinking' = '{}'::jsonb;

-- 统计清理结果（可选，方便验证）
DO $$
DECLARE
    cleaned_count INT;
BEGIN
    SELECT COUNT(*) INTO cleaned_count
    FROM message
    WHERE kind = 'message'
      AND role = 'assistant'
      AND metadata IS NOT NULL
      AND NOT (metadata ? 'thinking');

    RAISE NOTICE '清理完成，当前 assistant 消息中不含 thinking 字段的记录数: %', cleaned_count;
END $$;

COMMIT;
