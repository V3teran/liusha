import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { TaskFilters } from '@/api/types'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

// 任务列表筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/分页应作为 URL state」，
// 与 useTrafficFilters 同构）：刷新/后退/分享链接都能还原筛选与翻页位置。
//
// 参数名：source / status / search / page / size。
// source 口径对齐后端：定时克隆归 manual（主动），auto 仅被动聚合。
export interface TaskUrlState {
  filters: TaskFilters
  page: number // 1-based
  size: number
  setFilters: (next: TaskFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

const EMPTY_FILTERS: TaskFilters = { source: '', status: '', search: '', since: '', until: '' }

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

export function useTaskFilters(): TaskUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const size = useMemo(() => {
    const raw = numOr(searchParams.get('size'), DEFAULT_PAGE_SIZE)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
  }, [searchParams])
  const filters = useMemo<TaskFilters>(
    () => ({
      source: searchParams.get('source') ?? '',
      status: searchParams.get('status') ?? '',
      search: searchParams.get('search') ?? '',
      since: searchParams.get('since') ?? '',
      until: searchParams.get('until') ?? '',
    }),
    [searchParams],
  )

  const setFilters = (next: TaskFilters) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      const setOrDel = (key: string, val: string) => (val ? p.set(key, val) : p.delete(key))
      setOrDel('source', next.source)
      setOrDel('status', next.status)
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
