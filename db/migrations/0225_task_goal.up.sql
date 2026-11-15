-- 0225：任务目标只有一处事实源。
--
-- 任务的目标此前分在三处：task.brief 存用户原文，run 消息另带一份「原文 + 附件清单」，
-- 根目标由第一次 run 用那份消息创建；代理捕获的 task.brief 只是一句占位说明，完整说明
-- 只在消息里。三处各自为政：恢复重投用 task.brief（丢附件清单），目标无从调整。现在
-- task.goal 就是智能体看到的目标，与根目标描述由同一个事务写入（任务创建时一同建根目标），
-- run 不再携带输入。
--
-- 存量：goal 对齐根目标描述（智能体实际看到的）；没有根目标的任务补建一个，状态跟随
-- 任务；agent_run.input 删除。

ALTER TABLE public.task RENAME COLUMN brief TO goal;

UPDATE public.task t
   SET goal = r.description
  FROM (SELECT DISTINCT ON (task_id) task_id, content->>'description' AS description
          FROM public.exploration_node
         WHERE kind = 'objective' AND coalesce(metadata->>'source_type', '') <> 'planner'
         ORDER BY task_id, created_at, id) r
 WHERE r.task_id = t.id AND coalesce(r.description, '') <> '';

INSERT INTO public.exploration_node (id, task_id, kind, state, content, metadata, created_at, updated_at)
SELECT gen_random_uuid(), t.id, 'objective',
       CASE t.status WHEN 'active' THEN 'open' ELSE t.status END,
       jsonb_build_object('description', t.goal),
       '{"source_type": "user", "priority": "high"}'::jsonb,
       t.created_at, t.created_at
  FROM public.task t
 WHERE NOT EXISTS (SELECT 1 FROM public.exploration_node n
                    WHERE n.task_id = t.id AND n.kind = 'objective'
                      AND coalesce(n.metadata->>'source_type', '') <> 'planner');

ALTER TABLE public.agent_run DROP COLUMN input;

COMMENT ON COLUMN public.task.goal IS '任务目标：智能体看到的目标全文（人工下发为用户输入加附件清单，代理捕获为合成的分析说明）。与根目标描述由同一事务写入，可在运行中调整。';
