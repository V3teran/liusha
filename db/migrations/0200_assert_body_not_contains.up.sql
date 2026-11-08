-- 0200：复现配方断言键 body_absent → body_not_contains。
-- 2026-10-31（命名治理）之前 Go 侧以 body_absent 序列化配方断言并回写落库；现行代码只认
-- body_not_contains。两键语义完全相同（响应体须不含任一子串），按键名原地改写即可，
-- 改写后读路径不再需要别名。仅触碰确实含旧键的行。
UPDATE public.exploration_node
   SET content = regexp_replace(content::text, '"body_absent"(\s*):', '"body_not_contains"\1:', 'g')::jsonb
 WHERE content::text LIKE '%"body_absent"%';

UPDATE public.exploration_verification
   SET primitives = regexp_replace(primitives::text, '"body_absent"(\s*):', '"body_not_contains"\1:', 'g')::jsonb
 WHERE primitives::text LIKE '%"body_absent"%';

UPDATE public.finding
   SET repro = regexp_replace(repro::text, '"body_absent"(\s*):', '"body_not_contains"\1:', 'g')::jsonb
 WHERE repro IS NOT NULL AND repro::text LIKE '%"body_absent"%';
