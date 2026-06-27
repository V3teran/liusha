-- 回滚：重建 conversation.status 列 + CHECK 约束 + 索引（与 0066 一致）。
ALTER TABLE conversation ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'active'
	CHECK (status = ANY (ARRAY['active'::text, 'archived'::text]));
CREATE INDEX IF NOT EXISTS conversation_status_idx ON conversation (status, updated_at DESC);
