UPDATE public.message
   SET metadata = (metadata - 'results' - 'hypotheses')
       || jsonb_build_object('promoted', metadata->'results', 'attempts', metadata->'hypotheses')
 WHERE metadata->>'kind' = 'task_status'
   AND metadata ?| ARRAY['results', 'hypotheses'];

UPDATE public.agent_run
   SET result = jsonb_build_object(
         'Steps', result->'actions',
         'Promoted', result->'results',
         'Attempts', result->'hypotheses',
         'StopReason', result->'stop_reason')
 WHERE result ?& ARRAY['actions', 'results', 'hypotheses', 'stop_reason'];
