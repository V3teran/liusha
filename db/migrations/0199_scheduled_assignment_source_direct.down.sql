-- 0199 回卷：定时克隆批次的来源 direct → proxy（恢复修正前的存量状态）。
UPDATE public.assignment SET source = 'proxy' WHERE schedule_id IS NOT NULL AND source = 'direct';
