-- 0216：每个任务只有一个根目标。
--
-- 根目标此前有三处创建逻辑：runner 下发时按简报里的每个主机各建一个（内容带
-- target_host，靠 (task_id, target_host) 唯一索引去重，恢复重投时仍会重复建）、认知
-- 循环在没有目标时兜底建、调整目标时兜底建，形状各不相同；动作只挂在最早的根目标下，
-- 其余根目标只是摆设（本地库实测一个任务有 12 个）。现在只由
-- explorationgraph.EnsureRootObjective 创建，内容只有目标描述；主机属于任务
-- （task.target_host）与领域，不进根目标。
--
-- 存量：每个任务保留最早的根目标，挂在其余根目标上的边改挂到它，再删除其余根目标；
-- 去掉目标内容里的 target_host；唯一索引改为「每个任务一个根目标」。

CREATE TEMP TABLE root_merge ON COMMIT DROP AS
SELECT n.id AS extra_id, k.keep_id
  FROM public.exploration_node n
  JOIN (SELECT DISTINCT ON (task_id) task_id, id AS keep_id
          FROM public.exploration_node
         WHERE kind = 'objective' AND coalesce(metadata->>'source_type', '') <> 'planner'
         ORDER BY task_id, created_at, id) k ON k.task_id = n.task_id
 WHERE n.kind = 'objective' AND coalesce(n.metadata->>'source_type', '') <> 'planner'
   AND n.id <> k.keep_id;

INSERT INTO public.exploration_edge (task_id, src_id, rel, dst_id, created_at)
SELECT e.task_id, m.keep_id, e.rel, e.dst_id, e.created_at
  FROM public.exploration_edge e JOIN root_merge m ON m.extra_id = e.src_id
 WHERE e.dst_id NOT IN (SELECT extra_id FROM root_merge) AND e.dst_id <> m.keep_id
ON CONFLICT DO NOTHING;

INSERT INTO public.exploration_edge (task_id, src_id, rel, dst_id, created_at)
SELECT e.task_id, e.src_id, e.rel, m.keep_id, e.created_at
  FROM public.exploration_edge e JOIN root_merge m ON m.extra_id = e.dst_id
 WHERE e.src_id NOT IN (SELECT extra_id FROM root_merge) AND e.src_id <> m.keep_id
ON CONFLICT DO NOTHING;

DELETE FROM public.exploration_node WHERE id IN (SELECT extra_id FROM root_merge);

UPDATE public.exploration_node
   SET content = content - 'target_host'
 WHERE kind = 'objective' AND content ? 'target_host';

DROP INDEX IF EXISTS public.exploration_objective_task_host_uniq;

CREATE UNIQUE INDEX exploration_root_objective_uniq ON public.exploration_node (task_id)
 WHERE kind = 'objective' AND coalesce(metadata->>'source_type', '') <> 'planner';
