-- 回滚 0064：重建 endpoint 表（0056+0057+0058 终态：无 status、含 name）。
-- 仅恢复结构，历史行数据不还原（攻击面可从 http_flow 重新派生）。

CREATE TABLE IF NOT EXISTS endpoint (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id        uuid        NOT NULL,
    host            text        NOT NULL,
    method          text        NOT NULL,
    path            text        NOT NULL,  -- 模板化（/user/1 → /user/:id）
    discovered_at   timestamptz NOT NULL DEFAULT now(),
    name            text,                  -- 界面功能名（可空，前端 fallback method+path）
    UNIQUE (owner_id, host, method, path)
);

CREATE INDEX IF NOT EXISTS endpoint_owner_host_idx ON endpoint(owner_id, host);
