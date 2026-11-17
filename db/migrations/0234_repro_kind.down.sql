-- 0234 降级：配方信封改回旧名。证据键不还原（status_code 与会话刷新记录里的同名键无法区分），作废的指纹键无法还原。

UPDATE public.exploration_node
   SET content = replace(replace(content::text,
                 '"kind": "http"', '"domain": "web"'),
                 '"kind": "steps"', '"domain": "generic"')::jsonb
 WHERE content::text LIKE '%"kind": "http"%' OR content::text LIKE '%"kind": "steps"%';

UPDATE public.exploration_verification
   SET repro = replace(replace(repro::text,
               '"kind": "http"', '"domain": "web"'),
               '"kind": "steps"', '"domain": "generic"')::jsonb
 WHERE repro::text LIKE '%"kind": "http"%' OR repro::text LIKE '%"kind": "steps"%';

UPDATE public.finding
   SET repro = replace(replace(repro::text,
               '"kind": "http"', '"domain": "web"'),
               '"kind": "steps"', '"domain": "generic"')::jsonb
 WHERE repro IS NOT NULL AND (repro::text LIKE '%"kind": "http"%' OR repro::text LIKE '%"kind": "steps"%');

COMMENT ON COLUMN public.finding.repro IS '通过复现门的复现配方（域信封 {domain, recipe, assert}），供复验与人读';
COMMENT ON COLUMN public.exploration_verification.repro IS '被验证假设的复现配方（域信封 {domain, recipe, assert}）';
