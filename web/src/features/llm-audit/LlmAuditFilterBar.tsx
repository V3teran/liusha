import type { ReactNode } from 'react'
import type { LLMInvocationFilters } from '@/api/types'
import { FilterBar } from '@/components/FilterBar'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { ScopeChip } from '@/features/relations/ScopeChip'
import { NO_SCOPE, scopeOf } from '@/features/relations/scope'
import { SearchInput } from '@/components/SearchInput'
import { useSearchDraft } from '@/hooks/useDebouncedSearch'

interface LlmAuditFilterBarProps {
  filters: LLMInvocationFilters
  roles: string[]
  models: string[]
  search: string
  onSearchChange: (value: string) => void
  onChange: (next: LLMInvocationFilters) => void
  onReset: () => void
  /** 按当前的筛选重取一遍列表（搜索框上的「刷新」） */
  onRefresh: () => void
  trailing?: ReactNode
}

/**
 * LlmAuditFilterBar - 模型日志筛选栏
 *
 * 全站统一筛选顺序（对齐 TrafficFilterBar/TaskFilterBar/发现页）：
 * 范围（从其它页面跳来时）→ 枚举下拉（粗→细：角色→模型）→ 关键词搜索 → 时间范围 → 布尔开关
 * → 清空 →（最右）分页。
 * 时间范围在 URL 中保存 datetime-local 原值，由 API 层（api/llmaudit.ts）发送前换算为 ISO。
 */
export function LlmAuditFilterBar({ filters, roles, models, search, onSearchChange, onChange, onReset, onRefresh, trailing }: LlmAuditFilterBarProps) {
  const query = useSearchDraft(search, onSearchChange)
  const hasFilter = !!(
    filters.task ||
    filters.conversation ||
    filters.role ||
    filters.model ||
    filters.onlyErr ||
    filters.start ||
    filters.end ||
    search
  )
  const scope = scopeOf({ task: filters.task, conversation: filters.conversation })

  return (
    <FilterBar label="模型日志筛选" canReset={hasFilter} onReset={onReset} trailing={trailing}>
      {scope && <ScopeChip scope={scope} onClear={() => onChange({ ...filters, ...NO_SCOPE })} />}
      <Select
        value={filters.role}
        onChange={(e) => onChange({ ...filters, role: e.target.value })}
        className="w-32"
        aria-label="按角色筛选"
      >
        <option value="">全部角色</option>
        {roles.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </Select>

      <Select
        value={filters.model}
        onChange={(e) => onChange({ ...filters, model: e.target.value })}
        className="w-44"
        aria-label="按模型筛选"
      >
        <option value="">全部模型</option>
        {models.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </Select>

      <SearchInput
        className="w-64"
        value={query.draft}
        onChange={query.setDraft}
        onSubmit={query.submit}
        onRefresh={onRefresh}
        placeholder="搜索任务、对话、批次 ID"
      />

      <DateTimeRange
        start={filters.start}
        end={filters.end}
        onChange={({ start, end }) => onChange({ ...filters, start, end })}
        label="调用时间"
      />

      <label className="inline-flex h-9 cursor-pointer select-none items-center gap-2 rounded-lg px-1 text-sm text-text-secondary transition-colors hover:text-text">
        <input
          type="checkbox"
          checked={filters.onlyErr}
          onChange={(e) => onChange({ ...filters, onlyErr: e.target.checked })}
          className="h-4 w-4 cursor-pointer rounded border-border accent-[var(--accent)]"
        />
        仅错误
      </label>
    </FilterBar>
  )
}
