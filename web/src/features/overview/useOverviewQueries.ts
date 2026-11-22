import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getOverview, getOverviewHealth, viewerTimeZone } from '@/api/overview'
import { listTasks } from '@/api/tasks'
import { listLogProcesses } from '@/api/logs'

/** 概览随时间变化：停在页面上时每 30 秒刷新一次（后台标签页不刷新，回到页面立即补取）。 */
export const OVERVIEW_REFRESH_MS = 30_000

/** 进行中的任务在概览页最多列出的条数（更多的去任务页看）。 */
export const ACTIVE_TASK_LIMIT = 6

const keys = {
  snapshot: (tz: string, days: number) => ['overview', 'snapshot', tz, days] as const,
  health: ['overview', 'health'] as const,
  active: ['overview', 'active-tasks'] as const,
}

export function useOverviewSnapshot(days: number) {
  const tz = viewerTimeZone()
  return useQuery({
    queryKey: keys.snapshot(tz, days),
    queryFn: () => getOverview(tz, days),
    refetchInterval: OVERVIEW_REFRESH_MS,
    placeholderData: keepPreviousData,
  })
}

export function useOverviewHealth() {
  return useQuery({
    queryKey: keys.health,
    queryFn: getOverviewHealth,
    refetchInterval: OVERVIEW_REFRESH_MS,
    placeholderData: keepPreviousData,
  })
}

/** 进行中的任务（运行与暂停），新建的在前；复用任务列表接口，与任务页同一口径。 */
export function useActiveTasks() {
  return useQuery({
    queryKey: keys.active,
    queryFn: () => listTasks(1, ACTIVE_TASK_LIMIT, { status: 'active' }),
    refetchInterval: OVERVIEW_REFRESH_MS,
    placeholderData: keepPreviousData,
  })
}

/**
 * 承载某个组件日志的进程：组件单独运行时是它自己的日志（runner.log），serve 部署（二进制、单镜像）
 * 全部组件在一个进程里，是 serve.log——同一种部署只会有其中一种布局。没有日志文件时为空。
 * 与日志页共用同一个查询（同一个键）。
 */
export function useLogProcessOf(): (component: string) => string | undefined {
  const { data } = useQuery({ queryKey: ['log-processes'], queryFn: listLogProcesses })
  const names = new Set((data?.processes ?? []).map((p) => p.name))
  return (component) => (names.has(component) ? component : names.has('serve') ? 'serve' : undefined)
}
