-- insight id 族列 text → uuid：与 task/assignment 等表类型对齐（值一直就是 uuid 字符串）。
-- 附带效果：assignment_id 上的四个 btree 索引体积减半、比较更快。

ALTER TABLE insight ALTER COLUMN id TYPE uuid USING id::uuid;
ALTER TABLE insight ALTER COLUMN assignment_id TYPE uuid USING assignment_id::uuid;
ALTER TABLE insight ALTER COLUMN source_task_id TYPE uuid USING source_task_id::uuid;
ALTER TABLE insight ALTER COLUMN source_agent_run_id TYPE uuid USING source_agent_run_id::uuid;
