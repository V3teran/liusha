-- 0221：节点的状态原因统一叫 state_reason。
--
-- 原键名 blocked_reason 名不副实：系统里没有「阻塞」状态，它记的是动作失败、被中止、
-- 停摆与任务结束时收口的原因。存量改名。

UPDATE public.exploration_node
   SET metadata = (metadata - 'blocked_reason') || jsonb_build_object('state_reason', metadata->'blocked_reason')
 WHERE metadata ? 'blocked_reason';

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源与优先级（source_type/priority）、动作调度（complexity/claimed_by）、当前状态的原因 state_reason、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
