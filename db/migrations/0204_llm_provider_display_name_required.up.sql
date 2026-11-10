-- 显示名称是 provider 在界面上唯一可读的标识（code 是 UUID）：列表、能力路由下拉、编辑标题
-- 都按它展示，因此必填，且不区分大小写唯一（同名在路由下拉里无从区分）。
-- 存量空名称按 Base URL 的主机名回填；回填后同名的，按创建顺序从第 2 个起追加 -2、-3…
UPDATE llm_provider SET display_name = btrim(display_name);
UPDATE llm_provider
SET display_name = coalesce(nullif(substring(base_url FROM '://([^/:?#]+)'), ''), 'provider')
WHERE display_name = '';
WITH ranked AS (
    SELECT code, row_number() OVER (PARTITION BY lower(display_name) ORDER BY created_at, code) AS n
    FROM llm_provider
)
UPDATE llm_provider p
SET display_name = p.display_name || '-' || r.n
FROM ranked r
WHERE p.code = r.code AND r.n > 1;

ALTER TABLE llm_provider ALTER COLUMN display_name DROP DEFAULT;
ALTER TABLE llm_provider
    ADD CONSTRAINT ck_llm_provider_display_name
        CHECK (display_name <> '' AND display_name = btrim(display_name));
CREATE UNIQUE INDEX uq_llm_provider_display_name ON llm_provider (lower(display_name));
