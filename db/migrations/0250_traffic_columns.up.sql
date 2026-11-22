-- 代理流量删去与其它列重复的协议信息，流量表删去只能用到主机条件的 (host, path) 索引。
--
-- 1. scheme 恒等于 url 开头的协议（两者由同一份快照拼出，scheme 为空时 url 也不带协议），
--    http_version 就在原始请求行里（request_raw 是完整报文）。界面不展示它们，唯一读取方
--    （合成代理任务目标）改从 url 取协议。两列删除。
-- 2. 路径筛选是 path LIKE（库的排序规则不是 C，btree 用不上 LIKE），(host, path) 索引实际只能
--    按 host 等值查找：代理流量上它被 (host, captured_at DESC) 覆盖；agent 流量总按任务查询，
--    host / path 只是任务内的附加条件（走 task_id 索引）。两个索引删除。
ALTER TABLE proxy_traffic DROP COLUMN scheme;
ALTER TABLE proxy_traffic DROP COLUMN http_version;
DROP INDEX IF EXISTS proxy_traffic_path_idx;
DROP INDEX IF EXISTS agent_traffic_path_idx;
