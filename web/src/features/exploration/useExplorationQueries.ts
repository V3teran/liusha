/**
 * 探索图模块 React Query hooks（对齐 useTaskQueries 约定）。探索图属于任务，一律按任务 ID 取。
 *
 * 探索图是运行时动态图，任务推进中节点持续增长：存在 running 节点时
 * 10s 轮询自动跟进，图静止后停轮询。
 */

import { useQuery } from '@tanstack/react-query'
import { getTaskGraph, getTaskVerifications } from '@/api/explorationgraph'
import { listTasks } from '@/api/tasks'

const explorationKeys = {
  graph: (taskId: string) => ['exploration', 'graph', taskId] as const,
  verifications: (taskId: string) => ['exploration', 'verifications', taskId] as const,
  latestTask: () => ['exploration', 'latest-task'] as const,
}

/** 任务的完整探索图。taskId 为空（未选定）不发请求。 */
export function useTaskGraph(taskId: string) {
  return useQuery({
    queryKey: explorationKeys.graph(taskId),
    queryFn: () => getTaskGraph(taskId),
    enabled: taskId !== '',
    refetchInterval: (query) =>
      query.state.data?.nodes.some((n) => n.state === 'running') ? 10_000 : false,
  })
}

/** 验证审计链（node_id → 最新判定）。best-effort：失败时消费侧按空表处理，不阻塞图。 */
export function useTaskVerifications(taskId: string) {
  return useQuery({
    queryKey: explorationKeys.verifications(taskId),
    queryFn: () => getTaskVerifications(taskId),
    enabled: taskId !== '',
    retry: false,
  })
}

/**
 * 默认任务：URL 未带 task 时取最新创建的任务（与任务列表同一排序）。
 * enabled 由调用方以「挂载时是否缺 id」的快照控制——用户清空选择后不应再自动回填。
 */
export function useLatestTaskId(enabled: boolean) {
  return useQuery({
    queryKey: explorationKeys.latestTask(),
    queryFn: async () => {
      const { items } = await listTasks(1, 1)
      return items[0]?.id ?? ''
    },
    enabled,
    staleTime: Infinity,
    retry: false,
  })
}
