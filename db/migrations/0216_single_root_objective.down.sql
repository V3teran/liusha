-- 合并掉的根目标无法还原；只恢复索引形态。
DROP INDEX IF EXISTS public.exploration_root_objective_uniq;
CREATE UNIQUE INDEX exploration_objective_task_host_uniq ON public.exploration_node USING btree (task_id, ((content ->> 'target_host'::text))) WHERE ((kind = 'objective'::text) AND (content ? 'target_host'::text));
