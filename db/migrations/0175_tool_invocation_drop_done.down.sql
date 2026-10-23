ALTER TABLE tool_invocation ADD COLUMN IF NOT EXISTS done boolean NOT NULL DEFAULT false;
