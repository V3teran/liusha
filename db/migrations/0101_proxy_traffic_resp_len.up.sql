-- 0101: proxy_traffic 用「响应长度」替换「耗时」。
--
-- 动机：前端流量列表把「耗时」列换成「长度」列——代理捕获场景下响应体字节数
--   比墙钟耗时更有分析价值（辨识大响应/空响应/异常体积），耗时对被动捕获意义不大。
--   耗时全链路移除（model/store/ingestor/handler/einotools proxy 映射一并去除），
--   不保留列（存量可丢，见 0075/0100 惯例）。
--
--   resp_len = 响应体字节数（已解压/截断后的 ResponseBody 长度），落库时由 ingestor 从
--   snapshot.ResponseBody 计算。旧行无法回填真实体积（raw 里含头，len(response_raw) 非体长），
--   默认 0。

ALTER TABLE proxy_traffic
    DROP COLUMN duration_ms,
    ADD  COLUMN resp_len bigint NOT NULL DEFAULT 0;

COMMENT ON COLUMN proxy_traffic.resp_len IS '响应体字节数（已解压/截断后），前端流量列表「长度」列';
