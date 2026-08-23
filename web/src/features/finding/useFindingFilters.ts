import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { FindingFilters } from '@/api/types'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

// 漏洞台账筛选态迁移进 URL search params（对齐流量模块 useTrafficFilters 的模式）：
// 刷新、前进/后退、把链接发给同事复核，筛选条件与翻页位置都能还原。
//
// 参数名：host / severity / status / source / scenario_id / page / size。
export interface FindingUrlState {
  filters: FindingFilters
  page: number // 1-based
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一
  setFilters: (next: FindingFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
}

const EMPTY_FILTERS: FindingFilters = {
  host: '',
  severity: '',
  status: '',
  source: '',
  scenario_id: '',
}

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

export function useFindingFilters(): FindingUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const size = useMemo(() => {
    const raw = numOr(searchParams.get('size'), DEFAULT_PAGE_SIZE)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
  }, [searchParams])
  const filters = useMemo<FindingFilters>(
    () => ({
      host: searchParams.get('host') ?? '',
      severity: searchParams.get('severity') ?? '',
      status: searchParams.get('status') ?? '',
      source: searchParams.get('source') ?? '',
      scenario_id: searchParams.get('scenario_id') ?? '',
    }),
    [searchParams],
  )

  const setFilters = (next: FindingFilters) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      const setOrDel = (key: string, val?: string) => (val ? p.set(key, val) : p.delete(key))
      setOrDel('host', next.host)
      setOrDel('severity', next.severity)
      setOrDel('status', next.status)
      setOrDel('source', next.source)
      setOrDel('scenario_id', next.scenario_id)
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

  return { filters: { ...EMPTY_FILTERS, ...filters }, page, size, setFilters, setPage, setSize }
}
