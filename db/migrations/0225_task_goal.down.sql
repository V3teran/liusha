ALTER TABLE public.agent_run ADD COLUMN input jsonb DEFAULT '{}'::jsonb NOT NULL;
ALTER TABLE public.task RENAME COLUMN goal TO brief;
COMMENT ON COLUMN public.task.brief IS '任务目标的自然语言描述（必填）：人工下发为用户输入的 brief；代理捕获为聚合器生成的分析说明。';
