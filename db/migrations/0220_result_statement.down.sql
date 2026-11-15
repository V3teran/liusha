UPDATE public.exploration_node
   SET content = (content - 'statement') || jsonb_build_object('summary', content->'statement')
 WHERE kind = 'result'
   AND content ? 'statement';
