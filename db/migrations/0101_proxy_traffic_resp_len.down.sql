-- 0101 down: 还原 duration_ms、删 resp_len。存量耗时无法回填（真实值已丢），还原列默认 0。

ALTER TABLE proxy_traffic
    DROP COLUMN resp_len,
    ADD  COLUMN duration_ms int NOT NULL DEFAULT 0;
