-- 0100 down: 还原 proxy_traffic 拆列结构 + 单列 consumed_by_task_id，删关联表。
-- raw 无法忠实回拆为 headers/body，旧结构列还原为空（存量本就可丢，见 up 注释）。

-- ── 1) 还原拆列结构
ALTER TABLE proxy_traffic
    DROP COLUMN request_raw,
    DROP COLUMN response_raw,
    DROP COLUMN content_type,
    DROP COLUMN http_version,
    ADD  COLUMN request_headers  jsonb,
    ADD  COLUMN request_body     bytea,
    ADD  COLUMN response_headers jsonb,
    ADD  COLUMN response_body    bytea,
    ADD  COLUMN consumed_by_task_id uuid REFERENCES task(id) ON DELETE SET NULL;

-- ── 2) 回填单列消费指针（M:N 降级取一条：最早消费的 task）
UPDATE proxy_traffic p
SET consumed_by_task_id = tt.task_id
FROM (
    SELECT DISTINCT ON (traffic_id) traffic_id, task_id
    FROM traffic_task
    ORDER BY traffic_id, created_at
) tt
WHERE tt.traffic_id = p.id;

-- ── 3) 还原部分索引
CREATE INDEX proxy_traffic_unconsumed_idx ON proxy_traffic (host)
    WHERE consumed_by_task_id IS NULL;

-- ── 4) 删关联表
DROP TABLE IF EXISTS traffic_task;
