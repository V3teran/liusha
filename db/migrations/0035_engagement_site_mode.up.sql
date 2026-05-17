-- 0035: 引入 site 模式——放开 engagement.mode CHECK 接受 'site'。
-- site engagement 用于「用户给 target_url + 自然语言任务简报」的主动扫描场景；
-- proxy 模式（流量驱动）保持不变。
--
-- 不动 engagement_active_proxy_uniq 唯一索引（WHERE mode='proxy'）——
-- site engagement 天然不受该唯一约束限制，支持并发多个 site 扫描。

ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode IN ('proxy', 'site'));
