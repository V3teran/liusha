-- 0079 down: 回滚 corpus，重建 lesson（最终结构：无 tenant_id、含 kind）。
-- 清库前提，不搬运数据。顺序与 up 相反：先删 corpus，再重建 lesson。

DROP TABLE IF EXISTS corpus;

CREATE TABLE lesson (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    host         text NOT NULL CHECK (host <> ''),
    content      text NOT NULL CHECK (content <> ''),
    content_hash text NOT NULL,
    priority     int  NOT NULL DEFAULT 5 CHECK (priority BETWEEN 1 AND 10),
    hit_count    int  NOT NULL DEFAULT 0,
    kind         text NOT NULL DEFAULT 'lesson' CHECK (kind IN ('lesson','hint')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX lesson_dedup  ON lesson (host, content_hash);
CREATE INDEX        lesson_lookup ON lesson (host, kind, priority DESC, updated_at DESC);

-- 扩展不 DROP：vector / pg_trgm 可能被其他表用，回滚 corpus 不应连带卸载扩展。
