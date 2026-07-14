-- 0077: 删旧 active_scan + passive_session 表（P0 收尾）。
--
-- 前序迁移已切走所有引用：
--   0074 hunter/finding/llm_invocation/tool_invocation 的 owner 列 → task_id
--   0075 http_flow 拆表（旧 owner_id 引用随之消失）
--   0076 conversation.scan_id → task_id
-- 此刻两张旧表已无任何外键指向，可安全 DROP。
--
-- passive_session.conversation_id 列随表 DROP 一并消失（反向持有关系被 0076 的对话持有取代）。
-- internal/activescan、internal/passivesession 包在 Go 侧删除（P0-2）。

DROP TABLE IF EXISTS active_scan;
DROP TABLE IF EXISTS passive_session;
