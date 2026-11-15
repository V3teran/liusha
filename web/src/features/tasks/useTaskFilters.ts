import type { TaskFilters } from '@/api/types'
import { useUrlFilterFields, useUrlPageState } from '@/hooks/useUrlPageState'

// 任务列表筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/分页应作为
// URL state」）：刷新、前进/后退、分享链接都能还原筛选与翻页位置。
//
// 参数名：task / conversation（范围：从其它页面跳来只看这一个任务）/ source / status / search /
// since / until / page / size。source 口径对齐后端：定时克隆归 direct，proxy 仅代理捕获聚合。
const FIELDS = {
  task: 'task',
  conversation: 'conversation',
  source: 'source',
  status: 'status',
  search: 'search',
  since: 'since',
  until: 'until',
} as const

/** 是否有生效中的筛选（含范围）：筛选栏的「清空筛选」与列表的「无匹配」空态共用。 */
export function hasTaskFilter(f: TaskFilters): boolean {
  return Object.values(f).some(Boolean)
}

export interface TaskUrlState {
  filters: TaskFilters
  page: number // 1-based
  size: number
  setFilters: (next: TaskFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

export function useTaskFilters(): TaskUrlState {
  const { filters, setFilters, resetFilters } = useUrlFilterFields(FIELDS)
  const { page, size, setPage, setSize } = useUrlPageState()

  return { filters, page, size, setFilters, setPage, setSize, resetFilters }
}
