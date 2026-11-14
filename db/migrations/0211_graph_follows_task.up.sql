-- 0211：任务图跟随任务状态。
--
-- 此前只有 run 正常退出时才收口目标，而且靠停止原因的字符串匹配判断结局；run 超时或
-- 停机时写库用的 ctx 已取消，收口直接失败；动作从不收口；没有 run 在跑的终态路径
-- （停止暂停中的任务、恢复上限、心跳判死）根本不收口。现在任务状态迁移在同一事务
-- 内让图跟随（explorationgraph.SyncWithTask）：根目标跟随任务状态，任务结束时仍
-- open 的派生目标落 exhausted（完成）或 aborted（终止），未结束的动作落 aborted。
--
-- 存量按同一规则收口已结束任务的图。根目标不再有 failed（任务只有 completed /
-- aborted），旧数据里的 failed 根目标随任务落 aborted。

UPDATE public.exploration_node n
   SET state = t.status
  FROM public.task t
 WHERE t.id = n.task_id AND t.status IN ('completed', 'aborted')
   AND n.kind = 'objective' AND coalesce(n.metadata->>'source_type', '') <> 'planner'
   AND n.state IN ('open', 'failed');

UPDATE public.exploration_node n
   SET state = CASE t.status WHEN 'completed' THEN 'exhausted' ELSE 'aborted' END
  FROM public.task t
 WHERE t.id = n.task_id AND t.status IN ('completed', 'aborted')
   AND n.kind = 'objective' AND n.metadata->>'source_type' = 'planner'
   AND n.state IN ('open', 'failed');

UPDATE public.exploration_node n
   SET state = 'aborted',
       metadata = n.metadata || jsonb_build_object('blocked_reason',
                    CASE t.status WHEN 'completed' THEN '任务已完成' ELSE '任务已终止' END)
  FROM public.task t
 WHERE t.id = n.task_id AND t.status IN ('completed', 'aborted')
   AND n.kind = 'action' AND n.state IN ('open', 'running');
