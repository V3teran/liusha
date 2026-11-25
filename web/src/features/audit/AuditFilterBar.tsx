import type { ReactNode } from 'react'
import type { AuditFilters } from '@/api/audit'
import { FilterBar } from '@/components/FilterBar'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { SearchInput } from '@/components/SearchInput'
import { useSearchDraft } from '@/hooks/useDebouncedSearch'

interface AuditFilterBarProps {
  filters: AuditFilters
  actions: string[]
  search: string
  onSearchChange: (value: string) => void
  onChange: (next: AuditFilters) => void
  onReset: () => void
  /** 按当前的筛选重取一遍列表（搜索框上的「刷新」） */
  onRefresh: () => void
  trailing?: ReactNode
}

/**
 * AuditFilterBar - 审计筛选栏。全站统一筛选顺序：枚举下拉（动作）→ 关键词搜索（操作者）
 * → 时间范围 → 清空 →（最右）分页。搜索框自带 300ms 防抖；时间范围在 URL 中保存
 * datetime-local 原值，由 API 层发送前换算为 ISO。
 */
export function AuditFilterBar({ filters, actions, search, onSearchChange, onChange, onReset, onRefresh, trailing }: AuditFilterBarProps) {
  const query = useSearchDraft(search, onSearchChange)
  const hasFilter = !!(filters.action || filters.start || filters.end || search)

  return (
    <FilterBar label="审计筛选" canReset={hasFilter} onReset={onReset} trailing={trailing}>
      <Select
        value={filters.action}
        onChange={(e) => onChange({ ...filters, action: e.target.value })}
        className="w-36"
        aria-label="按动作筛选"
      >
        <option value="">全部动作</option>
        {actions.map((a) => (
          <option key={a} value={a}>
            {a}
          </option>
        ))}
      </Select>

      <SearchInput
        className="w-64"
        value={query.draft}
        onChange={query.setDraft}
        onSubmit={query.submit}
        onRefresh={onRefresh}
        placeholder="搜索操作者…"
        ariaLabel="按操作者筛选"
      />

      <DateTimeRange
        start={filters.start}
        end={filters.end}
        onChange={({ start, end }) => onChange({ ...filters, start, end })}
        label="操作时间"
      />
    </FilterBar>
  )
}
