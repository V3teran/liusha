-- 退役 endpoint 表：攻击面迁移到 http_flow 派生（strix 单一真相源模型）。
--
-- 背景：endpoint 表只有 method+path（无参数），靠 planner 手动 write_endpoint 转写 recon
-- 所见，易漏且与 http_flow 对"浏览过的路由"双写冗余。Phase 1 后 CLI 工具流量经 mitmproxy
-- 入 http_flow(source=internal)，攻击面改由 sitemap.Projector 从 http_flow DistinctRoutes
-- 去重派生（参数自动入库、单一真相源）。endpoint 表/write_endpoint/read_endpoints 一并退役。
--
-- 表数据无需保留备查（攻击面可随时从 http_flow 重新派生）；down 重建空表结构供回滚。

DROP TABLE IF EXISTS endpoint;
