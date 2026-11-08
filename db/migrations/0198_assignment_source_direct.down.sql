-- 0198 回卷：下发来源取值 direct → self。

ALTER TABLE public.assignment DROP CONSTRAINT assignment_source_check;

UPDATE public.assignment SET source = 'self' WHERE source = 'direct';

ALTER TABLE public.assignment
    ADD CONSTRAINT assignment_source_check CHECK ((source = ANY (ARRAY['self'::text, 'proxy'::text])));
