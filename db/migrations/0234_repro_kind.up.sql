-- 0234：复现配方信封 {domain, recipe, assert} → {kind, recipe, assert}；复现证据的
-- exploit_status_code / exploit_duration_ms → status_code / duration_ms；坐实指纹改为
-- 中立形状后，结果节点上旧形状的指纹键作废。
--
-- 信封里的这个字段说的是配方长什么样、怎样复现（一个 HTTP 请求 / 一段自然语言步骤），
-- 不是业务领域；叫 domain 会和章程「域指引」的业务领域混为一谈。取值同步改名：
-- web → http（配方就是一个 HTTP 请求），generic → steps（配方是步骤描述）。
-- 证据里「exploit」是渗透测试的行话，验证请求的响应码与耗时与对照请求的 baseline_*
-- 对称命名即可。
--
-- 改写按 jsonb 文本做精确键值替换（同 0200）：JSON 字符串内容里的引号是转义的，不会误中。
-- 证据嵌在多层（顶层、replays[].evidence、finding.evaluation.evidence），文本替换一次覆盖。

UPDATE public.exploration_node
   SET content = replace(replace(content::text,
                 '"domain": "web"', '"kind": "http"'),
                 '"domain": "generic"', '"kind": "steps"')::jsonb
 WHERE content::text LIKE '%"domain": "web"%' OR content::text LIKE '%"domain": "generic"%';

UPDATE public.exploration_verification
   SET repro = replace(replace(repro::text,
               '"domain": "web"', '"kind": "http"'),
               '"domain": "generic"', '"kind": "steps"')::jsonb
 WHERE repro::text LIKE '%"domain": "web"%' OR repro::text LIKE '%"domain": "generic"%';

UPDATE public.exploration_verification
   SET evidence = replace(replace(replace(replace(evidence::text,
                  '"domain": "web"', '"kind": "http"'),
                  '"domain": "generic"', '"kind": "steps"'),
                  '"exploit_status_code":', '"status_code":'),
                  '"exploit_duration_ms":', '"duration_ms":')::jsonb
 WHERE evidence::text ~ '"domain": "(web|generic)"|"exploit_(status_code|duration_ms)":';

UPDATE public.finding
   SET repro = replace(replace(repro::text,
               '"domain": "web"', '"kind": "http"'),
               '"domain": "generic"', '"kind": "steps"')::jsonb
 WHERE repro IS NOT NULL AND (repro::text LIKE '%"domain": "web"%' OR repro::text LIKE '%"domain": "generic"%');

UPDATE public.finding
   SET evaluation = replace(replace(replace(replace(evaluation::text,
                    '"domain": "web"', '"kind": "http"'),
                    '"domain": "generic"', '"kind": "steps"'),
                    '"exploit_status_code":', '"status_code":'),
                    '"exploit_duration_ms":', '"duration_ms":')::jsonb
 WHERE evaluation::text ~ '"domain": "(web|generic)"|"exploit_(status_code|duration_ms)":';

-- 旧指纹键是「类别|方法|主机|路径|参数」，新键是「类别|对象|作用点」，二者永不相等：
-- 留着只是没有读者能用的数据。
UPDATE public.exploration_node
   SET metadata = metadata - 'claim_fingerprint'
 WHERE kind = 'result' AND metadata ? 'claim_fingerprint';

COMMENT ON COLUMN public.finding.repro IS '通过复现门的复现配方（信封 {kind, recipe, assert}），供复验与人读';
COMMENT ON COLUMN public.exploration_verification.repro IS '被验证假设的复现配方（信封 {kind, recipe, assert}）';
