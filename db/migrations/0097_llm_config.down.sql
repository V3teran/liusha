-- 0097 down: 删 LLM 配置表（FK 逆序：route → alias → provider）。
-- 事实源迁回 yaml 后重启由 seed insert-only 重填。
DROP TABLE IF EXISTS llm_role_route;
DROP TABLE IF EXISTS llm_alias;
DROP TABLE IF EXISTS llm_provider;
