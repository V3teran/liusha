DROP INDEX public.conversation_one_per_task;
CREATE INDEX conversation_task_idx ON public.conversation USING btree (task_id) WHERE (task_id IS NOT NULL);
