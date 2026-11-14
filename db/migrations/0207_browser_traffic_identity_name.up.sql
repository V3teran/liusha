-- 0207：浏览器流量的身份戳改为身份名。浏览器服务原以「<任务ID>-<身份名>」为身份并据此盖戳，
-- 而 agent 在工具里使用、按身份过滤流量时给的是身份名，过滤永远对不上。身份现只在任务内
-- 有意义（任务隔离由沙盒按任务目录完成），流量记身份名；存量的任务前缀一并去掉。
UPDATE public.agent_traffic
   SET identity = substr(identity, length(task_id::text) + 2)
 WHERE tool = 'browser'
   AND starts_with(identity, task_id::text || '-');

COMMENT ON COLUMN public.agent_traffic.identity IS '身份名（任务内有效，如 default、admin）：浏览器流量为发起请求的浏览器身份；其余工具为空';
