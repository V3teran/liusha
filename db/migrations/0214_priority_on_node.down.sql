UPDATE public.exploration_node
   SET content = content || jsonb_build_object('priority', metadata->>'priority')
 WHERE kind IN ('observation', 'result')
   AND metadata ? 'priority';
