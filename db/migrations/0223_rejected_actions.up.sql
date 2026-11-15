-- 0223：「中止」拆为已中止与已否决。
--
-- 此前动作只有一个 aborted：监察者判定不该做的（有判断）与任务结束时收口的（无判断）
-- 混在一起，规划者一律当作「禁止等效重提」——任务停止后续接时，只是被打断的工作也被
-- 当成禁区。现在：
--   - aborted：没做完就被收回，不含对错判断（任务收口、目标调整撤回），可按当前目标重提；
--   - rejected：监察者或用户判定这一步不该做，附原因与否决方（rejected_by），禁止等效重提。
--
-- 存量：收口写入的原因只有「任务已完成」「任务已终止」与空（0210 清过原因），其余带原因
-- 的 aborted 动作都是监察者的否决。监察决策名 kill_action 改为 reject_action，用户自定义
-- 的监察章程里的旧名一并改写。

ALTER TABLE public.exploration_node DROP CONSTRAINT ck_exploration_node_state;
ALTER TABLE public.exploration_node ADD CONSTRAINT ck_exploration_node_state CHECK (
    state IS NULL OR state = ANY (ARRAY['open', 'running', 'completed', 'failed', 'exhausted', 'aborted', 'rejected', 'superseded'])
);

UPDATE public.exploration_node
   SET state = 'rejected',
       metadata = metadata || jsonb_build_object('rejected_by', 'monitor')
 WHERE kind = 'action'
   AND state = 'aborted'
   AND coalesce(metadata->>'state_reason', '') NOT IN ('', '任务已完成', '任务已终止');

UPDATE public.agent_override
   SET fields = replace(fields::text, 'kill_action', 'reject_action')::jsonb
 WHERE agent_code = 'monitor' AND fields::text LIKE '%kill_action%';

COMMENT ON COLUMN public.exploration_node.state IS 'action/objective 的状态：open/running/completed/failed/exhausted/aborted（没做完即被收回，不含对错判断）/rejected（action 专用：被监察者或用户否决，见 metadata.rejected_by）/superseded（派生目标被吸收）';
COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源与优先级（source_type/priority）、动作调度（complexity/claimed_by）、当前状态的原因 state_reason、动作的否决方 rejected_by（monitor/user）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
