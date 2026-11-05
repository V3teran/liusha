-- 0194: 目标（objective）节点按 批次+host 防重——恢复重投路径的双根缺陷修复。
--
-- 背景：恢复重投（runner 硬杀后自动重投 planner）会第二次执行 executeTask →
-- onboard 对同一 host 再次 CreateNode objective → 图里出现两个 open 的同 host
-- 目标。执行期消费方（planner 读目标 / adjust_goal 改目标 / 完成检测）全部取
-- objectives[0]，多根时可能读写到错误节点。
--
-- 三步：
--   1) open/running 的重复（保留最早，其余标 failed）
--   2) 已终态（aborted/failed/completed）的重复 → 删除死行
--   3) 部分唯一索引：(task_id, content->>'target_host') WHERE kind='objective'
--      AND content ? 'target_host'——并发路径也兜底；无 target_host 的目标
--      （ensureRootObjective 的纯 description 根）不在索引内，不受影响

-- 1) open/running 重复 → 标 failed（保留每组最早节点）
UPDATE exploration_node e
SET state = 'failed',
    metadata = COALESCE(e.metadata, '{}'::jsonb) || jsonb_build_object(
        'failure_reason', '重复目标（恢复重投双建）去重',
        'deduped_at', NOW(),
        'kept_node', dup.keep_id
    )
FROM (
    SELECT min(id::text) AS keep_id, task_id, content->>'target_host' AS host
    FROM exploration_node
    WHERE kind = 'objective' AND content ? 'target_host'
    GROUP BY task_id, content->>'target_host'
    HAVING count(*) > 1
) dup
JOIN exploration_node e2 ON e2.task_id = dup.task_id
    AND e2.content->>'target_host' = dup.host
    AND e2.kind = 'objective'
WHERE e.kind = 'objective'
  AND e.content ? 'target_host'
  AND e.content->>'target_host' = dup.host
  AND e.task_id = dup.task_id
  AND e.id <> dup.keep_id::uuid
  AND e.state IN ('open', 'running');

-- 2) 已终态的重复 → 删除死行（保留每组最早节点）
DELETE FROM exploration_node e
USING (
    SELECT min(id::text) AS keep_id, task_id, content->>'target_host' AS host
    FROM exploration_node
    WHERE kind = 'objective' AND content ? 'target_host'
    GROUP BY task_id, content->>'target_host'
    HAVING count(*) > 1
) dup
JOIN exploration_node e2 ON e2.task_id = dup.task_id
    AND e2.content->>'target_host' = dup.host
    AND e2.kind = 'objective'
WHERE e.kind = 'objective'
  AND e.content ? 'target_host'
  AND e.content->>'target_host' = dup.host
  AND e.task_id = dup.task_id
  AND e.id <> dup.keep_id::uuid
  AND e.state NOT IN ('open', 'running');

-- 3) 唯一索引：同批次同 host 只允许一个目标（任何状态——重复即写入拒绝）
CREATE UNIQUE INDEX exploration_objective_task_host_uniq
    ON exploration_node (task_id, (content->>'target_host'))
    WHERE kind = 'objective' AND content ? 'target_host';
