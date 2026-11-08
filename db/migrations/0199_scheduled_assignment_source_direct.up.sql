-- 0199：定时克隆批次的来源 proxy → direct。
-- 定时模板的条目恒为用户编写的目标描述（唯一写入方 DispatchScheduled），调度器此前把克隆批次误写为 proxy，
-- 致对话列表与漏洞筛选把定时任务归入代理捕获、而任务表另行改判为人工下发。修正存量，各页面统一读真实值。
UPDATE public.assignment SET source = 'direct' WHERE schedule_id IS NOT NULL AND source = 'proxy';
