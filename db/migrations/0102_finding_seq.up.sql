-- 0102: finding 加对外顺序号 seq —— 稳定、单调、人可读的短标识（类 Jira PROJ-123 / GitHub #123）。
--
-- 动机：finding 主键是 uuid（gen_random_uuid()），适合系统内引用但对人不可扫读、不可口述。
--   报告/会话里需要一个短号引用某条漏洞（"确认下 #42 的复现"）。业界最佳实践：
--   内部主键仍用 uuid（分布式友好、不可猜），另立一个 bigserial 对外展示序号。
--
--   seq 全库单调递增（BIGSERIAL），跨 task/host 唯一。存量行按 created_at 顺序回填
--   （BIGSERIAL 建列时 PG 自动为已有行分配序列值，此处显式 ORDER 保证与时间同序）。

-- 先建列（bigserial 会自动为存量行分配值，但顺序不保证=created_at，故下一步显式重排）
ALTER TABLE finding ADD COLUMN seq bigserial;

-- 存量行按 created_at 重排 seq，使旧漏洞的短号与发现时间同序（新库无存量，此步 no-op）
WITH ordered AS (
    SELECT id, row_number() OVER (ORDER BY created_at, id) AS rn
    FROM finding
)
UPDATE finding f SET seq = o.rn FROM ordered o WHERE f.id = o.id;

-- 把序列游标推到当前 max(seq) 之后，避免后续 INSERT 与回填值撞号
SELECT setval(pg_get_serial_sequence('finding', 'seq'), COALESCE((SELECT max(seq) FROM finding), 0) + 1, false);

ALTER TABLE finding ADD CONSTRAINT finding_seq_unique UNIQUE (seq);

COMMENT ON COLUMN finding.seq IS '对外顺序号（bigserial，单调递增，报告/会话引用用）；内部主键仍是 id(uuid)';
