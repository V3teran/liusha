-- 0197：schema 注释对齐现行架构。
-- 0196 基线沿用了来源旧名（active/passive）与已退役的 agent 角色（traffic-analysis / exploitation），
-- 且 task.brief / task.target_host 的注释与 task.Create 的实际约束不符（brief 恒必填）。仅改注释，不动结构。

COMMENT ON COLUMN public.agent_run.role IS 'agent 角色枚举：planner / executor / evaluator / monitor（见 agent_run_role_check）。跟 asynq queue 路由（worker.RoleAgent）解耦。';

COMMENT ON COLUMN public.task.brief IS '任务目标的自然语言描述（必填）：自主下发为用户输入的 brief；代理模式为聚合器生成的分析说明。';

COMMENT ON COLUMN public.task.target_host IS '目标 host：代理模式建 task 时即填（被分析 host）；自主下发由 runner 从 brief 抽取后回填，可空。';

COMMENT ON TABLE public.traffic_task IS '代理流量↔代理模式 task 多对多消费关系（一条捕获可被多 task 复用）';
