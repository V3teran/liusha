-- 0042: 删除 passive_session / active_scan 的 *_count 冗余列。
--
-- 设计动因：
--   - flow_count / finding_count / agent_run_count 是 best-effort 维护的近似计数
--   - 增量写路径失败时仅 warn 不阻塞，Abort 时 SELECT count(*) 兜底重算
--   - 既然永远要重算，列本身无价值——读路径直接 SELECT count(*) 即可
--   - 删除可省 ~150 行 Go 代码（ownerCounter 接口 + WithCounter + Increment*Count）
--
-- 不可逆性：删列后已写入的值丢失；down 重建列但值为 0。

ALTER TABLE passive_session DROP COLUMN flow_count;
ALTER TABLE passive_session DROP COLUMN finding_count;
ALTER TABLE passive_session DROP COLUMN agent_run_count;

ALTER TABLE active_scan DROP COLUMN finding_count;
ALTER TABLE active_scan DROP COLUMN agent_run_count;
