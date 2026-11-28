-- 回滚：线索退回布尔信号 opens_deeper。三样（下一步 / 要弄清的结论 / 推进价值）没有依据
-- 可以还原。
ALTER TABLE exploration_node DROP CONSTRAINT ck_exploration_node_lead;

UPDATE exploration_node
   SET metadata = (metadata - 'lead') || jsonb_build_object('opens_deeper', 'true'::jsonb)
 WHERE metadata ? 'lead';

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源与优先级（source_type/priority）、动作调度（complexity/claimed_by）、当前状态的原因 state_reason、动作的否决方 rejected_by（monitor/user）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
