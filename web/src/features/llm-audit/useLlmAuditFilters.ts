import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { LLMInvocationFilters } from '@/api/types'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

// 筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/排序/分页应作为 URL state」）：
// 刷新页面、前进/后退、把链接发给同事复核，搜索词、筛选条件与翻页位置都能还原。
//
// 参数名：q / role / model / only_err / start / end / page / size。
// offset 分页（非游标）：对齐流量/漏洞模块的「共 X 条 + N/M 页 + 每页条数」体验。

export interface LlmAuditUrlState {
  query: string
  filters: LLMInvocationFilters
  page: number // 1-based
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一
  setQuery: (q: string) => void
  setFilters: (next: LLMInvocationFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

const EMPTY_FILTERS: LLMInvocationFilters = { role: '', model: '', onlyErr: false, start: '', end: '' }

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

export function useLlmAuditFilters(): LlmAuditUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const query = searchParams.get('q') ?? ''
  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const size = useMemo(() => {
    const raw = numOr(searchParams.get('size'), DEFAULT_PAGE_SIZE)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
  }, [searchParams])
  const filters = useMemo<LLMInvocationFilters>(
    () => ({
      role: searchParams.get('role') ?? '',
      model: searchParams.get('model') ?? '',
      onlyErr: searchParams.get('only_err') === '1',
      start: searchParams.get('start') ?? '',
      end: searchParams.get('end') ?? '',
    }),
    [searchParams],
  )

  const setQuery = (q: string) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (q) next.set('q', q)
      else next.delete('q')
      next.delete('page') // 换搜索词回到第一页
      return next
    })
  }

  const setFilters = (nextFilters: LLMInvocationFilters) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (nextFilters.role) next.set('role', nextFilters.role)
      else next.delete('role')
      if (nextFilters.model) next.set('model', nextFilters.model)
      else next.delete('model')
      if (nextFilters.onlyErr) next.set('only_err', '1')
      else next.delete('only_err')
      if (nextFilters.start) next.set('start', nextFilters.start)
      else next.delete('start')
      if (nextFilters.end) next.set('end', nextFilters.end)
      else next.delete('end')
      next.delete('page') // 筛选变化回到第一页
      return next
    })
  }

  const setPage = (next: number) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      if (next > 1) p.set('page', String(next))
      else p.delete('page')
      return p
    })
  }

  // 换每页条数：回到第一页（否则「第 3 页 · 100 条/页」可能落在数据末尾之外）。
  const setSize = (next: number) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      if (next !== DEFAULT_PAGE_SIZE) p.set('size', String(next))
      else p.delete('size')
      p.delete('page')
      return p
    })
  }

  const resetFilters = () => setFilters(EMPTY_FILTERS)

  return { query, filters, page, size, setQuery, setFilters, setPage, setSize, resetFilters }
}
