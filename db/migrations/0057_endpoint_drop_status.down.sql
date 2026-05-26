-- 回滚：恢复 status 字段 + check + 索引（与 0056 up 一致）
ALTER TABLE endpoint ADD COLUMN status text NOT NULL DEFAULT 'discovered';
ALTER TABLE endpoint ADD CONSTRAINT endpoint_status_check CHECK (status IN ('discovered', 'tested_vulnerable'));
CREATE INDEX endpoint_owner_status_idx ON endpoint(owner_id, status);
