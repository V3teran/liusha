-- 多轮对话 follow-up 会 Reopen 已终态的 active_scan（ended_at=NULL 复活续跑），导致墙钟
-- now-created 把「完成→追加」之间的用户停顿也算进耗时（虚高，实测 94min 含 ~60min 停顿）。
-- paused_ms 累计历次停顿时长（Reopen 时 += now-上次ended_at），WallclockMs 减去它 =
-- 纯 agent 工作耗时（排除停顿）。首次扫描 paused_ms=0，口径与原墙钟一致。
ALTER TABLE active_scan ADD COLUMN paused_ms bigint NOT NULL DEFAULT 0;
