-- 0236 降级：已建 ctf 任务的场景快照改回旧名称与措辞。场景表的内置行由降级后版本的出厂同步处理。

UPDATE public.task
   SET scenario_name = 'CTF 夺旗',
       scenario_body = replace(scenario_body, '靶场里解题，', '靶场里解题夺旗，')
 WHERE scenario = 'ctf' AND scenario_name = 'CTF';
