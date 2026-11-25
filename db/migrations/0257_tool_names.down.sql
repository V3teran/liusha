-- 回退：把三处的工具名改回旧名。回退的前提是程序也退回改名的版本（否则工具名又对不上）。

CREATE OR REPLACE FUNCTION pg_temp.rename_tool_back(name text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE name
    WHEN 'corpus_search' THEN 'search_corpus'
    WHEN 'traffic_list' THEN 'list_traffic'
    WHEN 'traffic_view' THEN 'view_traffic'
    WHEN 'credential_read' THEN 'read_credentials'
    WHEN 'credential_write' THEN 'write_credential'
    WHEN 'skill_read' THEN 'read_skill'
    WHEN 'command_run' THEN 'run_command'
    WHEN 'browser_drive' THEN 'drive_browser'
    WHEN 'graph_read' THEN 'observe_state'
    WHEN 'progress_evaluate' THEN 'evaluate_progress'
    WHEN 'task_survey' THEN 'get_global_state'
    WHEN 'decision_publish' THEN 'publish_decision'
    WHEN 'repro_replay' THEN 'replay_for_verification'
    ELSE name
  END
$$;

UPDATE public.agent_override
   SET fields = jsonb_set(fields, '{function_tools}', jsonb_build_object(
         'added',   COALESCE((SELECT jsonb_agg(to_jsonb(pg_temp.rename_tool_back(v #>> '{}'))) FROM jsonb_array_elements(coalesce(fields->'function_tools'->'added', '[]'::jsonb)) v), '[]'::jsonb),
         'removed', COALESCE((SELECT jsonb_agg(to_jsonb(pg_temp.rename_tool_back(v #>> '{}'))) FROM jsonb_array_elements(coalesce(fields->'function_tools'->'removed', '[]'::jsonb)) v), '[]'::jsonb)
       ))
 WHERE fields ? 'function_tools';

UPDATE public.tool_invocation
   SET tool_name = pg_temp.rename_tool_back(tool_name)
 WHERE tool_name IN ('corpus_search','traffic_list','traffic_view','credential_read',
                     'credential_write','skill_read','command_run','browser_drive','graph_read',
                     'progress_evaluate','task_survey','decision_publish','repro_replay');

UPDATE public.message
   SET metadata = jsonb_set(metadata, '{tool_name}', to_jsonb(pg_temp.rename_tool_back(metadata->>'tool_name')))
 WHERE kind = 'event'
   AND metadata->>'kind' IN ('tool_call', 'tool_result')
   AND metadata->>'tool_name' IN ('corpus_search','traffic_list','traffic_view','credential_read',
                                  'credential_write','skill_read','command_run','browser_drive','graph_read',
                                  'progress_evaluate','task_survey','decision_publish','repro_replay');
