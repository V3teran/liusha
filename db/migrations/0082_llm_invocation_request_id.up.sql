-- request_id：每次 LLM 调用的稳定关联键（跨系统对账/排障用，参考行业审计表设计）。
-- 沿用项目惯例——标识符由数据库生成（gen_random_uuid()），非 Go 侧生成；
-- 加列时 volatile 默认值会触发整表重写，逐行补全历史数据（比全落空串更有意义）。
ALTER TABLE llm_invocation ADD COLUMN request_id text NOT NULL DEFAULT gen_random_uuid()::text;
CREATE INDEX llm_invocation_request_id_idx ON llm_invocation (request_id);
