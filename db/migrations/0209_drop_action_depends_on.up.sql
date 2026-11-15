-- 0209：动作之间不再有执行依赖。
--
-- depends_on 只约束执行顺序、不传递信息，且只能指向已存在的动作；被依赖的动作一旦
-- 失败或被中止，依赖方永久停在 open，又因非终态阻止任务收敛。先后关系改由增量规划
-- 表达：需要前一步结果的动作在前一步的观察落图后才被提出。

UPDATE public.exploration_node
   SET metadata = metadata - 'depends_on',
       content = content - 'depends_on'
 WHERE kind = 'action'
   AND (metadata ? 'depends_on' OR content ? 'depends_on');

COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：身份（task_id/source_type/source_id/priority）、动作调度（complexity/blocked_reason）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
