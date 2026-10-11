-- 重命名 exploration_node 的触发器和函数，清理 wm（world model）命名残留
--
-- 背景：0148 迁移已将表名从 wm_node 改为 exploration_node，
-- 但触发器和函数名仍保留 wm 前缀。

-- 1. 重命名触发器
ALTER TRIGGER trg_wm_node_updated_at ON exploration_node
  RENAME TO trg_expl_node_updated_at;

ALTER TRIGGER trigger_increment_wm_node_version ON exploration_node
  RENAME TO trg_expl_node_increment_version;

-- 2. 重命名函数
ALTER FUNCTION update_wm_node_updated_at()
  RENAME TO update_exploration_node_updated_at;

ALTER FUNCTION increment_wm_node_version()
  RENAME TO increment_exploration_node_version;

-- 验证
DO $$
DECLARE
  trigger_count int;
  function_count int;
BEGIN
  -- 检查触发器
  SELECT COUNT(*) INTO trigger_count
  FROM pg_trigger
  WHERE tgrelid = 'exploration_node'::regclass
    AND tgname LIKE '%wm%';

  IF trigger_count > 0 THEN
    RAISE EXCEPTION '仍有 % 个触发器包含 wm 命名', trigger_count;
  END IF;

  -- 检查函数
  SELECT COUNT(*) INTO function_count
  FROM pg_proc
  WHERE proname LIKE '%wm%node%';

  IF function_count > 0 THEN
    RAISE EXCEPTION '仍有 % 个函数包含 wm 命名', function_count;
  END IF;

  RAISE NOTICE '✓ 触发器和函数命名已统一为 exploration 前缀';
END $$;
