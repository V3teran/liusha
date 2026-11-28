-- 回滚：约束撤掉。动作上清掉的 priority 没有依据可以还原。
ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_urgent;
ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_priority;
