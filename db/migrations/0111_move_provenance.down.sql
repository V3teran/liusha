-- 回滚边类型约束到原有3种
ALTER TABLE wm_edge DROP CONSTRAINT IF EXISTS wm_edge_rel_check;
ALTER TABLE wm_edge ADD CONSTRAINT wm_edge_rel_check
    CHECK (rel IN ('derives', 'enables', 'on'));

-- 删除 wm_move 表
DROP TABLE IF EXISTS wm_move;
