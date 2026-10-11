-- 逆操作：移除 confidence 枚举键。
-- 旧浮点编码（metadata._confidence）在正向迁移中已按裁决事实重建，浮点副本不可恢复。
UPDATE exploration_node
SET metadata = metadata - 'confidence'
WHERE COALESCE(metadata, '{}'::jsonb) ? 'confidence';
