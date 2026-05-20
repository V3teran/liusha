-- 0043: 表 rename agent_run → agent_task。
--
-- 设计动因：
--   - "run" 是动名词，语义虚——"run of what?"
--   - 其他实体都是名词（finding/flow/note/lesson/credential），唯独 agent_run 是动名词
--   - "task" 是更标准的"待执行/已执行单元"，和 worker queue (asynq) 概念对齐
--
-- PG ALTER TABLE RENAME 自动级联：
--   - 主键约束 agent_run_pkey → agent_task_pkey（PG 自动改名）
--   - finding.agent_run_id / llm_invocation.agent_run_id FK 引用更新（列名不动）
--   - 索引 agent_run_owner_idx 名字也需手动 rename 保持命名一致性

ALTER TABLE agent_run RENAME TO agent_task;
ALTER INDEX agent_run_owner_idx RENAME TO agent_task_owner_idx;
