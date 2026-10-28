import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { createTasks, getTaskDetail, listTasks } from '@/api/client'
import type { TaskFilters } from '@/api/types'

// 任务模块 React Query hooks（对齐 useTrafficQueries 约定）：
// queryKey 含 filters+page+size → 任一变化即新查询，过期请求自动被丢弃；
// keepPreviousData 让筛选/翻页时旧数据先留屏不闪骨架。

const taskKeys = {
  list: (filters: TaskFilters, page: number, size: number) => ['tasks', 'list', filters, page, size] as const,
  detail: (id: string | null) => ['tasks', 'detail', id] as const,
}

/** 任务列表。列表里有 active 任务时 15s 轮询（进度/状态自动前进），否则静态。 */
export function useTaskList(filters: TaskFilters, page: number, size: number) {
  return useQuery({
    queryKey: taskKeys.list(filters, page, size),
    queryFn: () => listTasks(page, size, filters),
    placeholderData: keepPreviousData,
    refetchInterval: (query) => {
      const items = query.state.data?.items
      return items?.some((t) => t.status === 'active') ? 15_000 : false
    },
  })
}

/** 单任务详情（详情板）。id 为 null（未选中）不发请求。 */
export function useTaskDetail(id: string | null) {
  return useQuery({
    queryKey: taskKeys.detail(id),
    queryFn: () => getTaskDetail(id!),
    enabled: id != null,
    refetchInterval: (query) => (query.state.data?.status === 'active' ? 10_000 : false),
  })
}

/** 「新建任务」mutation key（组件内 useMutation 直用 createTasks，此导出供列表失效）。 */
export const TASK_LIST_KEY = ['tasks', 'list'] as const
export { createTasks }
