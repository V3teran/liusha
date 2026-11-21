-- 代理流量按捕获时间统计（概览的代理一项：近一小时的请求数）。已有的索引都以主机打头，
-- 按时间范围统计用不上，流量随代理使用持续增长。
CREATE INDEX IF NOT EXISTS proxy_traffic_captured_at_idx ON proxy_traffic (captured_at);
