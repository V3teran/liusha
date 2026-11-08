-- 0198：下发来源取值 self → direct（自主下发 = 用户直接给出目标；与路由 /conversations/direct 同名）。
-- 先放宽约束再搬数据，避免 UPDATE 期间违反旧 CHECK。

ALTER TABLE public.assignment DROP CONSTRAINT assignment_source_check;

UPDATE public.assignment SET source = 'direct' WHERE source = 'self';

ALTER TABLE public.assignment
    ADD CONSTRAINT assignment_source_check CHECK ((source = ANY (ARRAY['direct'::text, 'proxy'::text])));
