-- 0227：批次与定时模板的名称在写入时确定。
--
-- 名称此前可以不填，读取侧各自推导：任务列表取本行任务的目标（同一批次的不同行显示成
-- 不同的批次名），工作空间取首个任务的目标，定时模板抽屉取首个目标。现在由下发入口在
-- 写入时补齐（未填写时取首个目标的前 40 个字，与对话发起的批次同一规则），读取侧直接用。
--
-- 存量：没写名称的按同一规则补齐——首个目标的前 40 个字，代理捕获批次取主机；都没有时
-- 取批次内首个任务的目标。之后名称不得为空。

UPDATE public.assignment a
   SET title = COALESCE(
         NULLIF(left(a.payload->0->>'brief', 40), ''),
         NULLIF(a.payload->0->>'host', ''),
         NULLIF(left((SELECT t.goal FROM public.task t WHERE t.assignment_id = a.id
                       ORDER BY t.created_at, t.id LIMIT 1), 40), ''),
         '未命名批次')
 WHERE a.title = '';

UPDATE public.cron_schedule s
   SET title = COALESCE(NULLIF(left(s.payload->0->>'brief', 40), ''), '未命名模板')
 WHERE s.title = '';

ALTER TABLE public.assignment ALTER COLUMN title DROP DEFAULT;
ALTER TABLE public.assignment ADD CONSTRAINT assignment_title_not_empty CHECK (title <> '');
ALTER TABLE public.cron_schedule ALTER COLUMN title DROP DEFAULT;
ALTER TABLE public.cron_schedule ADD CONSTRAINT cron_schedule_title_not_empty CHECK (title <> '');

COMMENT ON COLUMN public.assignment.title IS '批次名：下发时填写，未填写取首个目标的前 40 个字（代理捕获批次为主机）。不为空。';
COMMENT ON COLUMN public.cron_schedule.title IS '模板名（触发时作为批次名）：未填写取首个目标的前 40 个字。不为空。';
