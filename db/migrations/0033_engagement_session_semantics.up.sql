-- 0033: engagement 语义升级为「渗透会话」（1 engagement 可挂 N host）
--
-- 背景：之前 engagement 按 target_host 单字段+唯一索引建，1 engagement ↔ 1 host
-- 严格绑死。这与 proxy 模式实际语义不符——代理开启期间任意 host 流量都属
-- 同一个 engagement（24h 时间窗内），需多 host 共享。
--
-- 新设计：
--   - target_host 字段 + 唯一索引删除
--   - scope: jsonb 描述会话作用域
--       proxy 模式: {} 或 {"any": true}            ← 接受任意 host
--       browser 模式: {"hosts": ["example.com"]}    ← 限定具体 host
--   - expires_at: timestamptz 控制 proxy session 时间窗
--       proxy 模式: created_at + 24h
--       browser 模式: null（扫完即终止，无 TTL）
--   - active 唯一性收紧到 proxy 模式（同时只 1 个 active proxy session）
--     browser 模式不强制唯一，可并行多个独立扫描

-- 1) 删旧 host 维度约束/字段
DROP INDEX IF EXISTS engagement_active_uniq;
ALTER TABLE engagement DROP COLUMN target_host;

-- 2) 加新字段
ALTER TABLE engagement ADD COLUMN scope jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE engagement ADD COLUMN expires_at timestamptz;

-- 3) proxy 模式新唯一约束：同时只 1 个 active proxy session
CREATE UNIQUE INDEX engagement_active_proxy_uniq
  ON engagement ((1)) WHERE status = 'active' AND mode = 'proxy';

-- 4) 加 expires_at 索引（Rotator 轮转判断）
CREATE INDEX engagement_expires_idx ON engagement (expires_at)
  WHERE status = 'active' AND expires_at IS NOT NULL;
