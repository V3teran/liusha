-- 0228：删除 finding.source_traffic_id。
--
-- 这一列声明了「漏洞来自哪条流量」（可指向代理流量或 agent 流量任一表，没有外键），但从没有
-- 任何代码写入它，存量全部为空。留着它会让「流量 → 漏洞」看起来是一个可查询的关系；
-- 漏洞属于任务，从流量只能经消费它的任务找到漏洞。

DROP INDEX public.finding_source_traffic_idx;
ALTER TABLE public.finding DROP COLUMN source_traffic_id;
