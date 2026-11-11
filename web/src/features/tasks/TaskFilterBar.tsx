import { type ReactNode, useEffect, useState } from 'react'
import type { TaskFilters } from '@/api/types'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { isoToLocalInput, syncIsoWithLocalInput } from '@/lib/datetime'
import { SOURCE_OPTIONS } from '@/lib/source'

interface TaskFilterBarProps {
  filters: TaskFilters
  onChange: (next: TaskFilters) => void
  onReset: () => void
  trailing?: ReactNode
}

const STATUSES: Array<{ value: string; label: string }> = [
  { value: 'active', label: '运行中' },
  { value: 'completed', label: '已完成' },
  { value: 'aborted', label: '已中止' },
]

/** 任务筛选栏（全站统一顺序）：来源/状态下拉 → 搜索（300ms 防抖）→ 创建时间范围 → 清空 →（右）分页。 */
export function TaskFilterBar({ filters, onChange, onReset, trailing }: TaskFilterBarProps) {
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

  const hasFilter = !!(filters.source || filters.status || filters.search || filters.since || filters.until)

  return (
    <FilterBar label="任务筛选" canReset={hasFilter} onReset={onReset} trailing={trailing}>
      <Select
        value={filters.source}
        onChange={(e) => onChange({ ...filters, source: e.target.value })}
        className="w-32"
        aria-label="按来源筛选"
      >
        <option value="">全部来源</option>
        {SOURCE_OPTIONS.map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </Select>

      <Select
        value={filters.status}
        onChange={(e) => onChange({ ...filters, status: e.target.value })}
        className="w-32"
        aria-label="按状态筛选"
      >
        <option value="">全部状态</option>
        {STATUSES.map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </Select>

      <SearchInput
        className="w-64"
        value={localSearch}
        onChange={setLocalSearch}
        placeholder="搜索目标、批次 ID"
        ariaLabel="搜索目标或批次 ID"
      />

      <DateTimeRange
        start={isoToLocalInput(filters.since)}
        end={isoToLocalInput(filters.until)}
        onChange={({ start, end }) => onChange({ ...filters, since: syncIsoWithLocalInput(filters.since, start), until: syncIsoWithLocalInput(filters.until, end) })}
        label="创建时间"
      />
    </FilterBar>
  )
}
