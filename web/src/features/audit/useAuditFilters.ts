import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useUrlPageState } from '@/hooks/useUrlPageState'
import type { AuditFilters } from '@/api/audit'

// 筛选态迁移进 URL search params（对齐模型调用页同款规则）：
// 参数名 q / action / start / end / page / size；offset 分页。

export interface AuditUrlState {
  query: string
  filters: AuditFilters
  page: number
  size: number
  setQuery: (q: string) => void
  setFilters: (next: AuditFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

export function useAuditFilters(): AuditUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const query = searchParams.get('search') ?? ''
  const { page, size, setPage, setSize } = useUrlPageState()
  const filters = useMemo<AuditFilters>(
    () => ({
      action: searchParams.get('action') ?? '',
      start: searchParams.get('start') ?? '',
      end: searchParams.get('end') ?? '',
    }),
    [searchParams],
  )

  // 搜索框输入改写当前历史记录（打字不是导航），换词回第一页
  const setQuery = (q: string) => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (q) next.set('search', q)
        else next.delete('search')
        next.delete('page')
        return next
      },
      { replace: true },
    )
  }

  const setFilters = (nextFilters: AuditFilters) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (nextFilters.action) next.set('action', nextFilters.action)
      else next.delete('action')
      if (nextFilters.start) next.set('start', nextFilters.start)
      else next.delete('start')
      if (nextFilters.end) next.set('end', nextFilters.end)
      else next.delete('end')
      next.delete('page')
      return next
    })
  }

  // 清空 = 筛选 + 搜索词一并复位，单次 setSearchParams 完成（函数式更新不排队）
  const resetFilters = () =>
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      for (const key of ['q', 'action', 'start', 'end', 'page']) next.delete(key)
      return next
    })

  return { query, filters, page, size, setQuery, setFilters, setPage, setSize, resetFilters }
}
