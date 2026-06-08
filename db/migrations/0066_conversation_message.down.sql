-- 回滚 0066：DROP message + conversation（message 先删，FK 依赖 conversation）。
-- 数据影响：所有对话与 agent 过程事件落库记录全丢（仅对话层，扫描/finding 在各自表不受影响）。

DROP INDEX IF EXISTS message_conversation_idx;
DROP TABLE IF EXISTS message;

DROP INDEX IF EXISTS conversation_scan_idx;
DROP INDEX IF EXISTS conversation_status_idx;
DROP TABLE IF EXISTS conversation;
