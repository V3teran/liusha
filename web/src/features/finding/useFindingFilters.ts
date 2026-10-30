import type { FindingFilters } from '@/api/types'
import { useUrlFilterFields, useUrlPageState } from '@/lib/useUrlPageState'

// 漏洞台账筛选态迁移进 URL search params：刷新、前进/后退、分享链接都能还原
// 筛选条件与翻页位置。参数名：host / severity / status / source / page / size。
const FIELDS = { host: 'host', severity: 'severity', status: 'status', source: 'source' } as const

export interface FindingUrlState {
  filters: FindingFilters
  page: number // 1-based
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一
  setFilters: (next: FindingFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

export function useFindingFilters(): FindingUrlState {
  const { filters, setFilters, resetFilters } = useUrlFilterFields(FIELDS)
  const { page, size, setPage, setSize } = useUrlPageState()

  return { filters, page, size, setFilters, setPage, setSize, resetFilters }
}
