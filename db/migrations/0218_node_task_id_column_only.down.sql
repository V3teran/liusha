UPDATE public.exploration_node
   SET metadata = metadata || jsonb_build_object('task_id', task_id::text);
