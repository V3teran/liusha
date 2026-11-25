import { type ReactNode } from 'react'
import type { HistoryOptions } from '@/hooks/useSelectedParam'
import { useSearchDraft } from '@/hooks/useDebouncedSearch'
import type { TrafficFilters } from '@/api/types'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { isoToLocalInput, syncIsoWithLocalInput } from '@/lib/datetime'

interface TrafficFilterBarProps {
  filters: TrafficFilters
  contentTypes: string[]
  onChange: (next: TrafficFilters, opts?: HistoryOptions) => void
  onReset: () => void
  /** 按当前的筛选重取一遍列表（搜索框上的「刷新」） */
  onRefresh: () => void
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
export function TrafficFilterBar({ filters, contentTypes, onChange, onReset, onRefresh, trailing }: TrafficFilterBarProps) {
  // 搜索词进 URL：输入停顿后才写，不逐键发请求（流量表是全系统增长最快的表）
  const search = useSearchDraft(filters.search, (next) => onChange({ ...filters, search: next }, { replace: true }), )

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
        className="w-40"
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
        className="w-56"
        value={search.draft}
        onChange={search.setDraft}
        onSubmit={search.submit}
        onRefresh={onRefresh}
        placeholder="搜索主机、URL（* 通配）"
        ariaLabel="搜索主机或 URL（支持 * 通配）"
      />

      <DateTimeRange
        start={isoToLocalInput(filters.since)}
        end={isoToLocalInput(filters.until)}
        onChange={({ start, end }) => onChange({ ...filters, since: syncIsoWithLocalInput(filters.since, start), until: syncIsoWithLocalInput(filters.until, end) })}
        label="捕获时间"
      />
    </FilterBar>
  )
}
