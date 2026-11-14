-- 0218：节点的任务归属只在 task_id 列一份。
--
-- 图存储的通用节点此前没有 TaskID 字段，任务归属经 metadata["task_id"] 夹带进存储层
-- 再写进列，于是同一个值在列与元数据 JSON 里各存一份。现在 core.GraphNode.TaskID 是
-- 正式字段，直接读写列；元数据里的副本删除。

UPDATE public.exploration_node
   SET metadata = metadata - 'task_id'
 WHERE metadata ? 'task_id';

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：来源与优先级（source_type/source_id/priority）、动作调度（complexity/claimed_by/blocked_reason）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
