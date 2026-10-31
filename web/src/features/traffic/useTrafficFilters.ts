import type { TrafficFilters } from '@/api/types'
import { useUrlFilterFields, useUrlPageState } from '@/hooks/useUrlPageState'

// 流量浏览筛选态迁移进 URL search params：刷新、前进/后退、把链接发给同事
// 复核，筛选条件与翻页位置都能还原。
//
// 参数名：method / content_type / status / search / since / until / page / size。
// 分页用 offset（page 号），非 keyset——proxy_traffic 全局浏览按 captured_at DESC，
// 允许任意跳页（有 total 可算总页数），故存单个 page 号即可，不需要游标栈。
const FIELDS = {
  method: 'method',
  contentType: 'content_type',
  statusClass: 'status',
  search: 'search',
  since: 'since',
  until: 'until',
} as const

export interface TrafficUrlState {
  filters: TrafficFilters
  page: number // 1-based
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一
  setFilters: (next: TrafficFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

export function useTrafficFilters(): TrafficUrlState {
  const { filters, setFilters, resetFilters } = useUrlFilterFields(FIELDS)
  const { page, size, setPage, setSize } = useUrlPageState()

  return { filters, page, size, setFilters, setPage, setSize, resetFilters }
}
