import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { TrafficFilters } from '@/api/types'

// 流量浏览筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/分页应作为 URL state」）：
// 刷新、前进/后退、把链接发给同事复核，筛选条件与翻页位置都能还原。
//
// 参数名：host / method / path / status_min / status_max / page。
// 分页用 offset（page 号），非 keyset——proxy_traffic 全局浏览按 captured_at DESC，
// 允许任意跳页（有 total 可算总页数），故存单个 page 号即可，不需要游标栈。
export interface TrafficUrlState {
  filters: TrafficFilters
  page: number // 1-based
  setFilters: (next: TrafficFilters) => void
  setPage: (page: number) => void
  resetFilters: () => void
}

const EMPTY_FILTERS: TrafficFilters = { host: '', method: '', path: '', statusMin: 0, statusMax: 0 }

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

export function useTrafficFilters(): TrafficUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const filters = useMemo<TrafficFilters>(
    () => ({
      host: searchParams.get('host') ?? '',
      method: searchParams.get('method') ?? '',
      path: searchParams.get('path') ?? '',
      statusMin: numOr(searchParams.get('status_min'), 0),
      statusMax: numOr(searchParams.get('status_max'), 0),
    }),
    [searchParams],
  )

  const setFilters = (next: TrafficFilters) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      const setOrDel = (key: string, val: string) => (val ? p.set(key, val) : p.delete(key))
      setOrDel('host', next.host)
      setOrDel('method', next.method)
      setOrDel('path', next.path)
      setOrDel('status_min', next.statusMin > 0 ? String(next.statusMin) : '')
      setOrDel('status_max', next.statusMax > 0 ? String(next.statusMax) : '')
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

  const resetFilters = () => setFilters(EMPTY_FILTERS)

  return { filters, page, setFilters, setPage, resetFilters }
}
