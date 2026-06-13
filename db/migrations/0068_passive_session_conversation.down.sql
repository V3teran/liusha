-- 0068 down: 移除 passive_session.conversation_id。
ALTER TABLE passive_session DROP COLUMN conversation_id;
