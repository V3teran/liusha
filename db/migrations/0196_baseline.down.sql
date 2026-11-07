-- 0196_baseline 的回卷 = 整库回卷（迁移链已压缩，无更早的可回卷历史）。
-- 仅限开发库使用；生产/存量环境严禁执行。
DROP EXTENSION IF EXISTS vector CASCADE;
DROP EXTENSION IF EXISTS pg_trgm CASCADE;
DROP EXTENSION IF EXISTS pgcrypto CASCADE;
DROP SCHEMA IF EXISTS public CASCADE;
CREATE SCHEMA public;
GRANT ALL ON SCHEMA public TO liusha;
GRANT ALL ON SCHEMA public TO public;
