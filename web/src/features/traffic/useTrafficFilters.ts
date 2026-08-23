import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { TrafficFilters } from '@/api/types'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

// 流量浏览筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/分页应作为 URL state」）：
// 刷新、前进/后退、把链接发给同事复核，筛选条件与翻页位置都能还原。
//
// 参数名：method / content_type / status / search / since / until / page / size。
// 分页用 offset（page 号），非 keyset——proxy_traffic 全局浏览按 captured_at DESC，
// 允许任意跳页（有 total 可算总页数），故存单个 page 号即可，不需要游标栈。
export interface TrafficUrlState {
  filters: TrafficFilters
  page: number // 1-based
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一
  setFilters: (next: TrafficFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

const EMPTY_FILTERS: TrafficFilters = {
  method: '',
  contentType: '',
  statusClass: '',
  search: '',
  since: '',
  until: '',
}

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

export function useTrafficFilters(): TrafficUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const size = useMemo(() => {
    const raw = numOr(searchParams.get('size'), DEFAULT_PAGE_SIZE)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
  }, [searchParams])
  const filters = useMemo<TrafficFilters>(
    () => ({
      method: searchParams.get('method') ?? '',
      contentType: searchParams.get('content_type') ?? '',
      statusClass: searchParams.get('status') ?? '',
      search: searchParams.get('search') ?? '',
      since: searchParams.get('since') ?? '',
      until: searchParams.get('until') ?? '',
    }),
    [searchParams],
  )

  const setFilters = (next: TrafficFilters) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      const setOrDel = (key: string, val: string) => (val ? p.set(key, val) : p.delete(key))
      setOrDel('method', next.method)
      setOrDel('content_type', next.contentType)
      setOrDel('status', next.statusClass)
      setOrDel('search', next.search)
      setOrDel('since', next.since)
      setOrDel('until', next.until)
      p.delete('page') // 筛选变化回到第一页
      return p
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

  return { filters, page, size, setFilters, setPage, setSize, resetFilters }
}
