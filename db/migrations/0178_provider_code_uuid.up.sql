-- provider 标识符改为系统生成的 UUID：display_name 承担人读角色后，
-- code 退化为纯技术标识，不再要求用户语义。存量 code 重生成，display_name 为空者回填旧 code。
-- 路由 FK 无 ON UPDATE CASCADE，先摘约束、重映射、再挂回。
CREATE TEMP TABLE llm_code_remap AS
  SELECT code AS old_code, gen_random_uuid()::text AS new_code FROM llm_provider;

ALTER TABLE llm_role_route DROP CONSTRAINT llm_role_route_provider_code_fkey;

UPDATE llm_provider p
SET code = r.new_code,
    display_name = CASE WHEN p.display_name = '' THEN r.old_code ELSE p.display_name END
FROM llm_code_remap r WHERE p.code = r.old_code;

UPDATE llm_role_route rt SET provider_code = r.new_code
FROM llm_code_remap r WHERE rt.provider_code = r.old_code;

ALTER TABLE llm_role_route
  ADD CONSTRAINT llm_role_route_provider_code_fkey
  FOREIGN KEY (provider_code) REFERENCES llm_provider(code) ON DELETE RESTRICT;

DROP TABLE llm_code_remap;
