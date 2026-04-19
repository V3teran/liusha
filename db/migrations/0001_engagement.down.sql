-- 0001 down: 回滚 engagement 表。pgvector 扩展留给其它迁移负责。
DROP TABLE IF EXISTS engagement;
