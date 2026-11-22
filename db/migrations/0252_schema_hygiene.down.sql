-- 回到整理之前。
CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;
ALTER TABLE llm_role_route DROP CONSTRAINT llm_role_route_role_check;
CREATE INDEX tool_invocation_name_idx ON tool_invocation (tool_name, created_at DESC);
CREATE INDEX audit_log_target_idx ON audit_log (target_kind, target_id, created_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor, created_at DESC);
CREATE INDEX audit_log_action_idx ON audit_log (action, created_at DESC);
ALTER TABLE account_session ADD COLUMN renewed_at timestamptz NOT NULL DEFAULT now();
