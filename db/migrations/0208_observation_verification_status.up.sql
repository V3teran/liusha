-- 0208：观察的图级状态改为验证状态。
--
-- 原 metadata.confidence 名为置信度，实为两件事的混合：假设的验证进度
-- （unverified/verified/refuted）与「不参与验证」（factual）；result 恒为 verified。
-- 验证结论还同时写在 metadata.verification_outcome，且验证重试耗尽出圈时被写成出圈
-- 原因，按 verification_outcome 取「已证伪假设」的消费方因此漏掉这些假设。
--
-- 现在只有假设观察带 metadata.verification_status（pending/confirmed/refuted），证伪原因
-- 只在 refute_reason；事实与 result 不带验证状态。issue 类观察取消（需要证明的问题走
-- 假设，环境反应记 behavior），存量 issue 观察并入 note。
--
-- 评估者（LLM judge）不可用的出圈原因 adjudication_failed 同时改名 judge_unavailable：
-- 评估者是智能体名，「裁决 / adjudication」不再作为它的别名。

-- 假设：confidence → verification_status；verification_outcome 里记着原因而缺 refute_reason
-- 的旧形状，原因迁到 refute_reason。
UPDATE public.exploration_node
   SET metadata = (metadata - 'confidence' - 'verification_outcome' - 'refuted_by')
       || jsonb_build_object('verification_status',
            CASE metadata->>'confidence'
              WHEN 'unverified' THEN 'pending'
              WHEN 'verified' THEN 'confirmed'
              ELSE 'refuted'
            END)
       || CASE
            WHEN metadata->>'confidence' = 'refuted'
             AND NOT metadata ? 'refute_reason'
             AND metadata->>'verification_outcome' NOT IN ('refuted', 'confirmed')
            THEN jsonb_build_object('refute_reason', metadata->>'verification_outcome')
            ELSE '{}'::jsonb
          END
 WHERE kind = 'observation'
   AND metadata->>'confidence' IN ('unverified', 'verified', 'refuted');

-- 评估者（LLM judge）不可用导致的出圈原因改名：adjudication_failed → judge_unavailable，
-- 与 evaluator.ErrJudgeUnavailable 同名；节点与审计链两处同改。
UPDATE public.exploration_node
   SET metadata = jsonb_set(metadata, '{refute_reason}', '"judge_unavailable"')
 WHERE kind = 'observation'
   AND metadata->>'refute_reason' = 'adjudication_failed';

UPDATE public.exploration_verification
   SET evidence = jsonb_set(evidence, '{refute_reason}', '"judge_unavailable"')
 WHERE evidence->>'refute_reason' = 'adjudication_failed';

-- 其余节点（事实观察、result）去掉置信度与旧的验证结论键。
UPDATE public.exploration_node
   SET metadata = metadata - 'confidence' - 'verification_outcome' - 'refuted_by'
 WHERE metadata ?| ARRAY['confidence', 'verification_outcome', 'refuted_by'];

-- 执行器自评置信度（low/medium/high）没有消费者，不再随坐实结果携带。
UPDATE public.exploration_node
   SET content = content - 'confidence'
 WHERE kind IN ('observation', 'result')
   AND content ? 'confidence';

UPDATE public.exploration_node
   SET content = jsonb_set(content, '{category}', '"note"')
 WHERE kind = 'observation'
   AND content->>'category' = 'issue';

ALTER TABLE public.exploration_node
    ADD CONSTRAINT ck_exploration_node_verification_status CHECK (
        NOT (metadata ? 'verification_status')
        OR (kind = 'observation'
            AND metadata->>'verification_status' IN ('pending', 'confirmed', 'refuted')));

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：身份（task_id/source_type/source_id/priority）、动作调度（complexity/depends_on/blocked_reason）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
