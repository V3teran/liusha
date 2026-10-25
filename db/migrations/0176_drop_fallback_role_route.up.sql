-- 删除全局备胎槽：兜底语义改为「各档重试耗尽后回落标准推理档（medium）部署」，
-- 不再有独立 role __fallback__。该 role 的历史路由行一并清理。
DELETE FROM llm_role_route WHERE role = '__fallback__';
