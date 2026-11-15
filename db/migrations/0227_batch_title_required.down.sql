ALTER TABLE public.cron_schedule DROP CONSTRAINT cron_schedule_title_not_empty;
ALTER TABLE public.cron_schedule ALTER COLUMN title SET DEFAULT '';
ALTER TABLE public.assignment DROP CONSTRAINT assignment_title_not_empty;
ALTER TABLE public.assignment ALTER COLUMN title SET DEFAULT '';
COMMENT ON COLUMN public.assignment.title IS NULL;
COMMENT ON COLUMN public.cron_schedule.title IS NULL;
