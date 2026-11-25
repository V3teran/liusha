-- 0257 内置函数工具名统一成「对象_动词」。
--
-- 工具名不只是出厂名单里的字符串，还是用户配置与存量数据里的字符串：智能体页上改过的工具名单按
-- 工具名记（agent_override.fields 的 added / removed），工具调用记录与会话时间线的事件也按工具名记。
-- 名字改了，这些地方不改的话——
--   用户当初去掉的工具会回来：removed 里记的旧名对不上新出厂名单，减不掉；
--   用户当初加上的会是个不存在的名字；
--   历史任务的页面上显示着已经没有的工具。
--
-- 只改「确实是工具名」的地方：
--   agent_override  只动 function_tools 的 added / removed。cli_tools、skills 是别的名单；charter 是
--                    用户写的章程，里面的工具名是正文，不动。
--   tool_invocation  tool_name 整列就是工具名。
--   message          只动 tool_call / tool_result 事件的 tool_name 键。text、thinking 是模型写的正文，
--                    args、result 是工具的输入输出——里面的字（包括恰好长得像旧工具名的）不动。
-- 沙盒里 browser-use 命令的报错原文里有一个事件名 drive_browser.browser…（Go 代码靠匹配它识别
-- 浏览器退化），它出现在 message 的 result 里而不是 tool_name 键，自然不会被波及。

CREATE OR REPLACE FUNCTION pg_temp.rename_tool(name text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE name
    WHEN 'search_corpus' THEN 'corpus_search'
    WHEN 'list_traffic' THEN 'traffic_list'
    WHEN 'view_traffic' THEN 'traffic_view'
    WHEN 'read_credentials' THEN 'credential_read'
    WHEN 'write_credential' THEN 'credential_write'
    WHEN 'read_skill' THEN 'skill_read'
    WHEN 'run_command' THEN 'command_run'
    WHEN 'drive_browser' THEN 'browser_drive'
    WHEN 'observe_state' THEN 'graph_read'
    WHEN 'evaluate_progress' THEN 'progress_evaluate'
    WHEN 'get_global_state' THEN 'task_survey'
    WHEN 'publish_decision' THEN 'decision_publish'
    WHEN 'replay_for_verification' THEN 'repro_replay'
    ELSE name
  END
$$;

-- 用户改过的函数工具名单：成员逐个换名。别的字段、别的名单原样保留。
-- 只动名单里确实有旧工具名的行：没改过名单的部署（没有这个字段、或 added / removed 为空、
-- 或里面都是别的名字）一个字节都不写——这条迁移改的是用户配置。
UPDATE public.agent_override
   SET fields = jsonb_set(fields, '{function_tools}', jsonb_build_object(
         'added',   COALESCE((SELECT jsonb_agg(to_jsonb(pg_temp.rename_tool(v #>> '{}'))) FROM jsonb_array_elements(coalesce(fields->'function_tools'->'added', '[]'::jsonb)) v), '[]'::jsonb),
         'removed', COALESCE((SELECT jsonb_agg(to_jsonb(pg_temp.rename_tool(v #>> '{}'))) FROM jsonb_array_elements(coalesce(fields->'function_tools'->'removed', '[]'::jsonb)) v), '[]'::jsonb)
       ))
 WHERE fields ? 'function_tools'
   AND (EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(fields->'function_tools'->'added', '[]'::jsonb)) v WHERE pg_temp.rename_tool(v #>> '{}') <> (v #>> '{}'))
     OR EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(fields->'function_tools'->'removed', '[]'::jsonb)) v WHERE pg_temp.rename_tool(v #>> '{}') <> (v #>> '{}')));

-- 工具调用记录。
UPDATE public.tool_invocation
   SET tool_name = pg_temp.rename_tool(tool_name)
 WHERE tool_name IN ('search_corpus','list_traffic','view_traffic','read_credentials',
                     'write_credential','read_skill','run_command','drive_browser','observe_state',
                     'evaluate_progress','get_global_state','publish_decision','replay_for_verification');

-- 会话时间线上的工具过程事件。
UPDATE public.message
   SET metadata = jsonb_set(metadata, '{tool_name}', to_jsonb(pg_temp.rename_tool(metadata->>'tool_name')))
 WHERE kind = 'event'
   AND metadata->>'kind' IN ('tool_call', 'tool_result')
   AND metadata->>'tool_name' IN ('search_corpus','list_traffic','view_traffic','read_credentials',
                                  'write_credential','read_skill','run_command','drive_browser','observe_state',
                                  'evaluate_progress','get_global_state','publish_decision','replay_for_verification');
