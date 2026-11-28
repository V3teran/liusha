-- 线索从事实观察上的布尔信号变成结构化字段。
--
-- opens_deeper 只说明「这条路走得通」，没处写下一步做什么、走完要弄清什么、走通了能换来
-- 什么，规划者只能对着事实陈述猜下一步；环境事实也被成批标成线索。现在线索是节点上的
-- 类型化字段 lead（见 explorationgraph.LeadSpec），写入时三样齐备才成线索。
--
-- 存量线索当时没有这三样可写，原样成为不带三样的线索：仍是线索（开合状态、跟进边、关闭
-- 批注都不动），摘要里照旧只列事实陈述。
UPDATE exploration_node
   SET metadata = (metadata - 'opens_deeper') || jsonb_build_object('lead', '{}'::jsonb)
 WHERE (metadata->>'opens_deeper') = 'true';
UPDATE exploration_node
   SET metadata = metadata - 'opens_deeper'
 WHERE metadata ? 'opens_deeper';

-- 线索只属于事实观察（假设在复现门里推进，不成线索），是个对象；关闭批注只落在线索上。
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_lead CHECK (
        (NOT (metadata ? 'lead')
            OR (kind = 'observation'
                AND jsonb_typeof(metadata -> 'lead') = 'object'
                AND NOT (metadata ? 'verification_status')))
        AND (NOT (metadata ? 'lead_closed')
            OR (kind = 'observation'
                AND jsonb_typeof(metadata -> 'lead_closed') = 'object'
                AND metadata ? 'lead'))
    );

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源 source_type、动作调度（complexity/claimed_by）、当前状态的原因 state_reason、动作的否决方 rejected_by（monitor/user）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、事实观察的线索 lead（下一步 / 要弄清的结论 / 推进价值，见 ck_exploration_node_lead）与规划层的关闭批注 lead_closed、结果的分析水位 analyzed 等';
