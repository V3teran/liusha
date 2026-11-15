-- 0210：中断不是失败。
--
-- run 被暂停、停机或超时打断时，在途动作此前因执行期 ctx 被取消而记成 failed（原因
-- 「context canceled」），runner 重启时又被统一标成 failed（「Runner 重启导致执行中
-- 断」）——规划者把它们当作此路不通。现在中断的动作交还 open，由下一个 run 重新执行；
-- 任务进入终态时仍未结束的动作落 aborted。
--
-- 存量改正：被打断的 failed 动作，任务仍 active 的退回 open，任务已结束的落 aborted。
-- 停摆判死（retryable 标记）是真正的失败，保持 failed。retryable / previous_state /
-- recovered_at 不再使用，一并删除。

UPDATE public.exploration_node n
   SET state = CASE WHEN t.status = 'active' THEN 'open' ELSE 'aborted' END,
       metadata = n.metadata - 'blocked_reason' - 'failure_reason'
 FROM public.task t
 WHERE t.id = n.task_id
   AND n.kind = 'action'
   AND n.state = 'failed'
   AND NOT n.metadata ? 'retryable'
   AND (coalesce(n.metadata->>'blocked_reason', '') LIKE '%context canceled%'
        OR n.metadata->>'failure_reason' = 'Runner 重启导致执行中断');

UPDATE public.exploration_node
   SET metadata = metadata - 'retryable' - 'previous_state' - 'recovered_at'
 WHERE kind = 'action'
   AND metadata ?| ARRAY['retryable', 'previous_state', 'recovered_at'];
