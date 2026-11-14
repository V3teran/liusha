-- 0213：run 报告的统计改为图上计数（explorationgraph.TaskStats），字段按图上的概念
-- 命名：执行过的动作 actions、坐实的结果 results、提出的假设 hypotheses。此前
-- 统计靠事件总线累计（可丢），字段叫 Steps / Promoted / Attempts，其中 steps 又与
-- 时间线「步数」（一次推理一步）重名。数值含义不变，存量改键名。

UPDATE public.message
   SET metadata = (metadata - 'promoted' - 'attempts')
       || jsonb_build_object('results', metadata->'promoted', 'hypotheses', metadata->'attempts')
 WHERE metadata->>'kind' = 'task_status'
   AND metadata ?| ARRAY['promoted', 'attempts'];

UPDATE public.agent_run
   SET result = jsonb_build_object(
         'actions', result->'Steps',
         'results', result->'Promoted',
         'hypotheses', result->'Attempts',
         'stop_reason', result->'StopReason')
 WHERE result ?& ARRAY['Steps', 'Promoted', 'Attempts', 'StopReason'];
