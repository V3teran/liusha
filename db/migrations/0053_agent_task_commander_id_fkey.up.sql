-- 0053: agent_task.commander_id 加自参照 FK（指向 agent_task.id）。
-- v1.1 之前一直裸列无约束——commander/striker 是同表父子关系，应用层保证一致性。
-- 加 FK 后 DB 层兜底：catch "commander_id 指向不存在的行" 这类 bug。
--
-- ON DELETE SET NULL：理论上若手动删 commander 行，striker 行保留但 commander_id 变 NULL
-- （生产无 DELETE 路径——asynq task 跑完只 SetDone 不删行；e2e setup 是整表 TRUNCATE
--  不触发 FK；本约束仅防御性兜底）。

ALTER TABLE agent_task
  ADD CONSTRAINT agent_task_commander_id_fkey
  FOREIGN KEY (commander_id)
  REFERENCES agent_task(id)
  ON DELETE SET NULL;
