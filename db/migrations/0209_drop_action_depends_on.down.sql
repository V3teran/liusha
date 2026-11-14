-- 依赖声明删除后无法还原（down 只恢复列注释）。
COMMENT ON COLUMN public.exploration_node.metadata IS '节点元数据：身份（task_id/source_type/source_id/priority）、动作调度（complexity/depends_on/blocked_reason）、假设观察的验证状态 verification_status（pending/confirmed/refuted，见 ck_exploration_node_verification_status）与证伪原因 refute_reason、结果的分析水位 analyzed 等';
