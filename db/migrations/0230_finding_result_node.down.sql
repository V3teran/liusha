-- 只还原表结构：0230 合并掉的重复记录无法还原。
DROP INDEX public.finding_task_result_uniq;
ALTER TABLE public.finding ADD COLUMN dedup_key text GENERATED ALWAYS AS (((((((((lower(host) || '|'::text) || lower(COALESCE(cwe_id, ''::text))) || '|'::text) || lower(regexp_replace(rtrim(split_part(COALESCE((target ->> 'path'::text), "substring"(summary, 1, 40)), '?'::text, 1), '/'::text), '/+'::text, '/'::text, 'g'::text))) || '|'::text) || lower(COALESCE((target ->> 'method'::text), ''::text))) || '|'::text) || lower(COALESCE((target ->> 'parameter'::text), ''::text)))) STORED;
CREATE UNIQUE INDEX finding_task_dedup_v3_uniq ON public.finding USING btree (task_id, dedup_key);
ALTER TABLE public.finding DROP COLUMN result_node_id;
