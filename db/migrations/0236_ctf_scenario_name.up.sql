-- 0236 出厂场景 ctf 的名称「CTF 夺旗」改为「CTF」，正文措辞「解题夺旗」改为「解题」。
--
-- 没改过的内置行不在这里改：出厂同步按「当前内容哈希 == seed_hash」认出它没改过并跟随新版；迁移若先改了
-- 内容，哈希对不上，会被误判为用户改过而停在旧版。用户改过内容的内置行由这里一并改名（基线不动，仍视为改过）。
-- 已建任务的场景快照同样改写，任务详情与续跑的提示词用新名称。

UPDATE public.scenario
   SET name = 'CTF',
       body = replace(body, '解题夺旗', '解题'),
       updated_at = now()
 WHERE code = 'ctf' AND is_builtin AND name = 'CTF 夺旗'
   AND seed_hash IS DISTINCT FROM
       encode(sha256(convert_to(name || chr(31) || description || chr(31) || body, 'UTF8')), 'hex');

UPDATE public.task
   SET scenario_name = 'CTF',
       scenario_body = replace(scenario_body, '解题夺旗', '解题')
 WHERE scenario = 'ctf' AND scenario_name = 'CTF 夺旗';
