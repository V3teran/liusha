-- 0005 down: 先 drop 子分区再 drop 分区父表，避免分区依赖问题。
DROP TABLE IF EXISTS http_flow_default;
DROP TABLE IF EXISTS http_flow;
