import { type ReactNode } from 'react'
import type { TaskFilters } from '@/api/types'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { isoToLocalInput, syncIsoWithLocalInput } from '@/lib/datetime'
import { SOURCE_OPTIONS } from '@/lib/source'
import { ScopeChip } from '@/features/relations/ScopeChip'
import { NO_SCOPE, scopeOf } from '@/features/relations/scope'
import { hasTaskFilter } from './useTaskFilters'
import type { HistoryOptions } from '@/hooks/useSelectedParam'
import { useSearchDraft } from '@/hooks/useDebouncedSearch'

interface TaskFilterBarProps {
  filters: TaskFilters
  onChange: (next: TaskFilters, opts?: HistoryOptions) => void
  onReset: () => void
  /** 按当前的筛选重取一遍列表（搜索框上的「刷新」） */
  onRefresh: () => void
  trailing?: ReactNode
}

const STATUSES: Array<{ value: string; label: string }> = [
  { value: 'active', label: '运行中' },
  { value: 'completed', label: '已完成' },
  { value: 'aborted', label: '已中止' },
]

/**
 * 任务筛选栏（全站统一顺序）：范围（从其它页面跳来时）→ 来源/状态下拉 → 搜索（300ms 防抖）→
 * 创建时间范围 → 清空 →（右）分页。
 */
export function TaskFilterBar({ filters, onChange, onReset, onRefresh, trailing }: TaskFilterBarProps) {
  // 搜索词进 URL（打字不是导航，改写当前历史）；输入停顿后才写，不逐字发请求
  const search = useSearchDraft(filters.search, (next) => onChange({ ...filters, search: next }, { replace: true }), )

  const scope = scopeOf(filters)
  return (
    <FilterBar label="任务筛选" canReset={hasTaskFilter(filters)} onReset={onReset} trailing={trailing}>
      {scope && <ScopeChip scope={scope} onClear={() => onChange({ ...filters, ...NO_SCOPE })} />}
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
        value={search.draft}
        onChange={search.setDraft}
        onSubmit={search.submit}
        onRefresh={onRefresh}
        placeholder="搜索目标、批次名"
        ariaLabel="搜索目标或批次名"
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
