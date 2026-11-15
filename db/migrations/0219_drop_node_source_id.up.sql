-- 0219：节点不再记 source_id。
--
-- source_id 没有任何读者，写法又各不相同：规划动作写字面量 "planner"、结果分析写
-- "result-analyzer"、观察写 run ID、结果写验证记录 ID、根目标写 "task_init"。节点的
-- 来源角色由 source_type 表达；结果与验证记录的关系沿推导链可得（结果 ← 假设 ←
-- 验证记录的 node_id）。存量删除。

UPDATE public.exploration_node
   SET metadata = metadata - 'source_id'
 WHERE metadata ? 'source_id';

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源与优先级（source_type/priority）、动作调度（complexity/claimed_by/blocked_reason）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
