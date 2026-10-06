ALTER TABLE llm_role_route DROP CONSTRAINT IF EXISTS llm_role_route_provider_code_fkey;
ALTER TABLE llm_role_route RENAME COLUMN provider_code TO provider_key;
ALTER TABLE llm_provider RENAME COLUMN code TO key;
ALTER TABLE llm_role_route
  ADD CONSTRAINT llm_role_route_provider_key_fkey
  FOREIGN KEY (provider_key) REFERENCES llm_provider(key) ON DELETE RESTRICT;
