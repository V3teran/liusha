-- 0005 down: 回滚 host_lesson 表
DROP INDEX IF EXISTS host_lesson_lookup;
DROP INDEX IF EXISTS host_lesson_dedup;
DROP TABLE IF EXISTS host_lesson;
