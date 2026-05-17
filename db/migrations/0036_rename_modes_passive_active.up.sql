-- 0036: 重命名 engagement.mode 为 passive/active（业界对仗术语）。
--
-- 旧命名 proxy/site 不对称（手段 vs 对象），新命名是反义词对：
--   proxy → passive (被动接收流量驱动)
--   site  → active  (主动用户指令驱动)
--
-- 同步重建唯一索引名 + WHERE 子句。
-- 注意：cmd/proxy MITM 进程名不动（那是真"代理"实体，与 mode 概念解耦）。

-- 1) 先 DROP 旧 CHECK（不然 UPDATE 写入 'passive'/'active' 会违反旧 CHECK）
ALTER TABLE engagement DROP CONSTRAINT engagement_mode_check;

-- 2) UPDATE 现有数据
UPDATE engagement SET mode='passive' WHERE mode='proxy';
UPDATE engagement SET mode='active'  WHERE mode='site';

-- 3) ADD 新 CHECK
ALTER TABLE engagement ADD CONSTRAINT engagement_mode_check CHECK (mode IN ('passive', 'active'));

-- 3) 重建唯一索引（同时只 1 个 active passive session = 旧"1 个 active proxy"语义）
DROP INDEX IF EXISTS engagement_active_proxy_uniq;
CREATE UNIQUE INDEX engagement_active_passive_uniq
  ON engagement ((1)) WHERE status = 'active' AND mode = 'passive';
