ALTER TABLE task ADD COLUMN IF NOT EXISTS heartbeat_at timestamp with time zone DEFAULT now() NOT NULL;
CREATE INDEX IF NOT EXISTS task_heartbeat_idx ON task USING btree (heartbeat_at) WHERE (status = 'active'::text);
