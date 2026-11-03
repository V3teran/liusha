UPDATE agent
SET function_tools = '["observe_state","evaluate_progress"]'::jsonb
WHERE code = 'planner'
  AND function_tools = '["observe_state","evaluate_progress","search_corpus"]'::jsonb;
