UPDATE public.agent_traffic
   SET identity = task_id::text || '-' || identity
 WHERE tool = 'browser'
   AND identity <> ''
   AND NOT starts_with(identity, task_id::text || '-');

COMMENT ON COLUMN public.agent_traffic.identity IS NULL;
