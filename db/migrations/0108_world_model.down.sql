-- 回滚 0108：逆序 DROP 世界模型三表（wm_edge 有 FK 指向 wm_node，先删边表/验证表再删节点表）。
DROP TABLE IF EXISTS wm_verification;
DROP TABLE IF EXISTS wm_edge;
DROP TABLE IF EXISTS wm_node;
