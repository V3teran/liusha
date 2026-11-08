-- 0197 回卷：恢复 0196 基线中的原注释文本。

COMMENT ON COLUMN public.agent_run.role IS 'agent 角色枚举：traffic-analysis（passive 单 agent）/ planner（active 父）/ exploitation（active 子）。跟 asynq queue 路由（worker.RoleAgent）解耦。';

COMMENT ON COLUMN public.task.brief IS 'active：用户自然语言 brief；passive：空';

COMMENT ON COLUMN public.task.target_host IS 'active 可空；passive 必填（被分析 host）';

COMMENT ON TABLE public.traffic_task IS '代理流量↔passive task 多对多消费关系（一条捕获可被多 task 复用）';
