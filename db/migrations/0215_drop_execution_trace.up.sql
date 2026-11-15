-- 0215：观察不再内嵌执行轨迹。
--
-- 每个动作结束后，执行者把整个任务的流量（至多 500 条，逐条读出含响应体）嵌进该
-- 动作的每一条观察：评估者、规划者、前端都不读它，本地库实测约占观察内容的大头。
-- 流量本就在 agent_traffic 里按任务可查。存量删除。

UPDATE public.exploration_node
   SET content = content - 'execution_trace'
 WHERE kind = 'observation'
   AND content ? 'execution_trace';
