-- host 的 "unknown" 占位符退役：地址是多形态的（host:port 只是最常见的
-- 一种），抽不到主机线索时落空串——空 = 诚实的「无此维度」，占位符是伪
-- 装的数据。非空 CHECK 约束正是占位符当初的成因，一并退役。
ALTER TABLE finding DROP CONSTRAINT finding_host_nonempty;
UPDATE finding SET host = '' WHERE host = 'unknown';
