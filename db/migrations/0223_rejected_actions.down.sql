UPDATE public.agent_override
   SET fields = replace(fields::text, 'reject_action', 'kill_action')::jsonb
 WHERE agent_code = 'monitor' AND fields::text LIKE '%reject_action%';

UPDATE public.exploration_node
   SET state = 'aborted', metadata = metadata - 'rejected_by'
 WHERE state = 'rejected';

ALTER TABLE public.exploration_node DROP CONSTRAINT ck_exploration_node_state;
ALTER TABLE public.exploration_node ADD CONSTRAINT ck_exploration_node_state CHECK (
    state IS NULL OR state = ANY (ARRAY['open', 'running', 'completed', 'failed', 'exhausted', 'aborted', 'superseded'])
);

COMMENT ON COLUMN public.exploration_node.state IS 'action 与 objective 的状态：open/running/completed/failed/exhausted/aborted，superseded 仅用于被吸收的派生目标；其余节点为 NULL';
COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源与优先级（source_type/priority）、动作调度（complexity/claimed_by）、当前状态的原因 state_reason、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
