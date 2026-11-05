-- 0196: 每任务至多一个存活 run（pending/running）——多生产者竞态的结构性护栏。
--
-- 背景：agent run 有四个生产方（下发展开 / 会话续接 / 暂停后继续 / 恢复监控
-- 重投），各自「先查后建」的纪律挡不住跨进程竞态（30s 巡检 vs 毫秒窗口）：
-- 一旦两个存活 run 同时入队，双认知循环并发——双倍 LLM 账单、探索图双写。
-- 把不变式下沉到事实源（部分唯一索引），违例写入直接被数据库拒绝。

-- 1) 存量清洗：同任务多个存活 run 保留最新，其余标 aborted（孤儿/竞态残渣）。
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY task_id ORDER BY created_at DESC, id) AS rn
    FROM agent_run
    WHERE status IN ('pending', 'running')
)
UPDATE agent_run r
SET status = 'aborted', updated_at = now()
FROM ranked
WHERE r.id = ranked.id AND ranked.rn > 1;

-- 2) 不变式：同任务同时至多一个 pending/running run。
CREATE UNIQUE INDEX agent_run_one_live_per_task
    ON agent_run (task_id)
    WHERE status IN ('pending', 'running');
