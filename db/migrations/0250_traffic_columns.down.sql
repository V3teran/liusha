-- 回到删列之前（scheme 从 url 回填，http_version 以空值恢复）。
CREATE INDEX agent_traffic_path_idx ON agent_traffic (host, path);
CREATE INDEX proxy_traffic_path_idx ON proxy_traffic (host, path);
ALTER TABLE proxy_traffic ADD COLUMN http_version text NOT NULL DEFAULT '';
ALTER TABLE proxy_traffic ADD COLUMN scheme text NOT NULL DEFAULT '';
UPDATE proxy_traffic SET scheme = substring(url FROM '^([a-z]+)://') WHERE url ~ '^[a-z]+://';
