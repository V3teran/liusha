-- 回滚触发器和函数重命名

ALTER TRIGGER trg_expl_node_updated_at ON exploration_node
  RENAME TO trg_wm_node_updated_at;

ALTER TRIGGER trg_expl_node_increment_version ON exploration_node
  RENAME TO trigger_increment_wm_node_version;

ALTER FUNCTION update_exploration_node_updated_at()
  RENAME TO update_wm_node_updated_at;

ALTER FUNCTION increment_exploration_node_version()
  RENAME TO increment_wm_node_version;
