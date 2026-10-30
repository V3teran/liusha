import type { TaskFilters } from '@/api/types'
import { useUrlFilterFields, useUrlPageState } from '@/lib/useUrlPageState'

// 任务列表筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/分页应作为
// URL state」）：刷新、前进/后退、分享链接都能还原筛选与翻页位置。
//
// 参数名：source / status / search / since / until / page / size。
// source 口径对齐后端：定时克隆归 manual（主动），auto 仅被动聚合。
const FIELDS = { source: 'source', status: 'status', search: 'search', since: 'since', until: 'until' } as const

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
