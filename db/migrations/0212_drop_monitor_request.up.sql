-- 0212：监察者的重规划请求直达规划者（planner.Agent.RequestReplan），不再写进根目标
-- 的 content.monitor_request——它是给规划者的一次性指令，不是图上的事实；此前靠
-- 「事件 + 写图」双写对冲事件总线满即丢弃，且写图是不带版本的读改写。存量删除。

UPDATE public.exploration_node
   SET content = content - 'monitor_request'
 WHERE kind = 'objective'
   AND content ? 'monitor_request';
