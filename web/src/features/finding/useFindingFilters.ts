import type { FindingFilters } from '@/api/types'
import { useUrlFilterFields, useUrlPageState } from '@/hooks/useUrlPageState'

// 漏洞台账筛选态迁移进 URL search params：刷新、前进/后退、分享链接都能还原
// 筛选条件与翻页位置。参数名：task / host / severity / status / source / search / page / size。
// task 是筛选栏里的「任务」下拉；任务详情与对话的「漏洞」跳转带着它进来（只看这个任务的漏洞）。
const FIELDS = { task: 'task', host: 'host', severity: 'severity', status: 'status', source: 'source', search: 'search' } as const

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
