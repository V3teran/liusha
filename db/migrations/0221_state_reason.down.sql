UPDATE public.exploration_node
   SET metadata = (metadata - 'state_reason') || jsonb_build_object('blocked_reason', metadata->'state_reason')
 WHERE metadata ? 'state_reason';
