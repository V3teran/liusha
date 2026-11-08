import { type ReactNode, useEffect, useState } from 'react'
import type { TrafficFilters } from '@/api/types'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { isoToLocalInput, syncIsoWithLocalInput } from '@/lib/datetime'

interface TrafficFilterBarProps {
  filters: TrafficFilters
  contentTypes: string[]
  onChange: (next: TrafficFilters) => void
  onReset: () => void
  trailing?: ReactNode
}

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

const STATUS_CLASSES: Array<{ value: string; label: string }> = [
  { value: '2', label: '2xx 成功' },
  { value: '3', label: '3xx 跳转' },
  { value: '4', label: '4xx 客户端' },
  { value: '5', label: '5xx 服务端' },
]

/**
 * TrafficFilterBar - 流量筛选栏：类型/方法/状态下拉 → 搜索（300ms 防抖）→ 时间范围 → 清空 →（右）分页。
 */
export function TrafficFilterBar({ filters, contentTypes, onChange, onReset, trailing }: TrafficFilterBarProps) {
  // 搜索框本地态 + 300ms 防抖：search 每键击直写 URL 会连环发请求（流量表是全系统增长最快的表），
  // 对齐 LlmAuditSearchBar 的防抖口径。外部变化（清空筛选/回退）即时回填本地态。
  const [localSearch, setLocalSearch] = useState(filters.search)
  useEffect(() => {
    setLocalSearch(filters.search)
  }, [filters.search])
  useEffect(() => {
    const timer = setTimeout(() => {
      if (localSearch !== filters.search) {
        onChange({ ...filters, search: localSearch })
      }
    }, 300)
    return () => clearTimeout(timer)
  }, [localSearch, filters, onChange])

  const hasFilter = !!(
    filters.method ||
    filters.contentType ||
    filters.statusClass ||
    filters.search ||
    filters.since ||
    filters.until
  )

  return (
    <FilterBar label="流量筛选" canReset={hasFilter} onReset={onReset} trailing={trailing}>
      <Select
        value={filters.contentType}
        onChange={(e) => onChange({ ...filters, contentType: e.target.value })}
        className="w-48"
        aria-label="按 Content-Type 筛选"
      >
        <option value="">全部类型</option>
        {contentTypes.map((ct) => (
          <option key={ct} value={ct}>
            {ct}
          </option>
        ))}
      </Select>

      <Select
        value={filters.method}
        onChange={(e) => onChange({ ...filters, method: e.target.value })}
        className="w-32"
        aria-label="按方法筛选"
      >
        <option value="">全部方法</option>
        {METHODS.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </Select>

      <Select
        value={filters.statusClass}
        onChange={(e) => onChange({ ...filters, statusClass: e.target.value })}
        className="w-36"
        aria-label="按状态大类筛选"
      >
        <option value="">全部状态</option>
        {STATUS_CLASSES.map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </Select>

      <SearchInput
        className="w-64"
        value={localSearch}
        onChange={setLocalSearch}
        placeholder="搜索主机、URL（* 通配）"
        ariaLabel="搜索主机或 URL（支持 * 通配）"
      />

      <DateTimeRange
        start={isoToLocalInput(filters.since)}
        end={isoToLocalInput(filters.until)}
        onChange={({ start, end }) => onChange({ ...filters, since: syncIsoWithLocalInput(filters.since, start), until: syncIsoWithLocalInput(filters.until, end) })}
        startLabel="起始时间"
        endLabel="结束时间"
      />
    </FilterBar>
  )
}
