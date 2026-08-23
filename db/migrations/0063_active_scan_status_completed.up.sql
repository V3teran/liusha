-- active_scan status 加 'completed' 自然完成终态。
--
-- 设计动机（model.go 包注释明写"active 跑完即终态"，但实现只接了 abort 终态）：
-- planner run 自然跑完时（handler_active.go 成功分支），无人翻转 active_scan.status，
-- scan 永久停留 'active'——UI / API 轮询 / 清理任务无法判断 scan 是否真正结束。
-- e2e 实测：4 个 agent 全 done，active_scan 仍 active。
--
-- 终态语义（不臆造 'failed'，YAGNI）：
--   completed = planner 自然完成（成功收尾）
--   aborted   = 用户主动停 / ctx 取消 / 错误（复用已有 Abort，带 error_message 区分）
-- agent 表保留 done/error/aborted 精确区分；active_scan 聚合层只需 active→completed/aborted。

ALTER TABLE active_scan DROP CONSTRAINT active_scan_status_check;
ALTER TABLE active_scan ADD CONSTRAINT active_scan_status_check
	CHECK (status = ANY (ARRAY['active'::text, 'aborted'::text, 'completed'::text]));
