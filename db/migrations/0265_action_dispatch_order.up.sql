-- 动作不再带优先级。
--
-- 指派动作时的「优先级」只在同是用户指派的几条之间排序：用户指派的动作本来就一律先于
-- 规划者排出的，这个档位没有东西可排。规划者给动作打的优先级此前已经去掉（0263）。此后
-- 优先级只属于主张：假设带，坐实后随结果进交付。
--
-- 动作的派发先后改由规则定：用户指派 → 加急 → 认领先索的 → 其余按创建先后。
UPDATE exploration_node
   SET metadata = metadata - 'priority'
 WHERE kind = 'action' AND metadata ? 'priority';

-- 优先级只属于主张。
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_priority CHECK (NOT (metadata ? 'priority') OR (kind IN ('observation', 'result')));

-- 加急只有规划者排出的动作可以带，值是 true（一轮至多一个，由规划者把关）。
ALTER TABLE exploration_node
    ADD CONSTRAINT ck_exploration_node_urgent CHECK (
        NOT (metadata ? 'urgent')
        OR (kind = 'action' AND (metadata ->> 'urgent') = 'true' AND metadata ->> 'source_type' = 'planner'));

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源 source_type、动作调度（complexity/claimed_by）、规划者给动作标的加急 urgent（见 ck_exploration_node_urgent）、当前状态的原因 state_reason、动作的否决方 rejected_by（monitor/user）、主张的优先级 priority（假设带，坐实后随结果；只属于 observation / result，见 ck_exploration_node_priority）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、事实观察的线索 lead（下一步 / 要弄清的结论 / 推进价值，见 ck_exploration_node_lead）与规划层的关闭批注 lead_closed、结果的分析水位 analyzed 等';
