-- http_flow 加 identity + tool 两字段：给每条 internal 流量盖"身份戳"和"工具戳"。
--
-- 动机：http_flow 此前无身份维度，导致"按身份从流量抽凭证写 redis"找不到对应身份
-- （一个 agent_id 下混着 admin/gordonb 多个会话、无标签）。盖戳后 LLM 能
-- list_flows(identity=X, tool=browser) 精确锁定身份 X 的浏览器已认证请求 → view_flow
-- 从 headers/query/body 三处抽全部凭证 → 按 name=X write/update redis（身份不搞错）。
--
-- identity：身份名（= browser_use 的 identity / 登录账号名）。
--   - browser 抓的流量：browser-svc 进程级 IDENTITY 已知，capture 时填
--   - CLI 工具流量（mitmproxy）：留 NULL（透明代理标不准，且 CLI 凭证走 redis/自登不需要）
-- tool：发起工具。
--   - browser 抓的：'browser'
--   - CLI 抓的：从 User-Agent 解析（curl / sqlmap / nuclei …），认不出存原始 UA / 'unknown'
--
-- 都 nullable（历史数据 + passive external 流量为 NULL）。owner-scoped 查询走现有 owner 索引。

ALTER TABLE http_flow ADD COLUMN identity text;
ALTER TABLE http_flow ADD COLUMN tool text;
