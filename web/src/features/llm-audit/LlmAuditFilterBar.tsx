import type { LLMInvocationFilters } from '@/api/types'
import { cn } from '@/lib/utils'
import { LlmAuditSearchBar } from './LlmAuditSearchBar'

interface LlmAuditFilterBarProps {
  filters: LLMInvocationFilters
  roles: string[]
  models: string[]
  search: string
  onSearchChange: (value: string) => void
  onChange: (next: LLMInvocationFilters) => void
  onReset: () => void
  trailing?: React.ReactNode
}

/**
 * LlmAuditFilterBar - LLM 审计筛选栏
 *
 * 全站统一筛选顺序（业界最佳实践，对齐 TrafficFilterBar/TaskFilterBar/漏洞页）：
 * 枚举下拉（粗→细：角色→模型）→ 关键词搜索 → 时间范围 → 布尔开关 → 清空 →（最右）分页。
 * 搜索框自带 300ms 防抖（LlmAuditSearchBar 内置）。
 */
export function LlmAuditFilterBar({ filters, roles, models, search, onSearchChange, onChange, onReset, trailing }: LlmAuditFilterBarProps) {
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end)

  const selectClass = cn(
    'h-8 rounded-md border border-border bg-surface px-2.5 text-sm text-text',
    'outline-none transition-all focus:border-accent focus:ring-1 focus:ring-accent/20'
  )

  const inputClass = cn(
    'h-8 rounded-md border border-border bg-surface px-2.5 text-sm text-text',
    'outline-none transition-all placeholder:text-muted',
    'focus:border-accent focus:ring-1 focus:ring-accent/20',
    '[&::-webkit-calendar-picker-indicator]:cursor-pointer [&::-webkit-calendar-picker-indicator]:opacity-60 [&::-webkit-calendar-picker-indicator]:hover:opacity-100'
  )

  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-border bg-surface/50 px-6 py-3">
      <select
        value={filters.role}
        onChange={(e) => onChange({ ...filters, role: e.target.value })}
        className={cn(selectClass, 'w-32')}
      >
        <option value="">全部角色</option>
        {roles.map((r) => (
          <option key={r} value={r}>
            {r}
          </option>
        ))}
      </select>

      <select
        value={filters.model}
        onChange={(e) => onChange({ ...filters, model: e.target.value })}
        className={cn(selectClass, 'w-40')}
      >
        <option value="">全部模型</option>
        {models.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>

      <div className="w-64">
        <LlmAuditSearchBar value={search} onChange={onSearchChange} />
      </div>

      <input
        id="filter-start"
        type="datetime-local"
        value={filters.start}
        onChange={(e) => onChange({ ...filters, start: e.target.value })}
        className={cn(inputClass, 'w-48')}
      />

      <span className="text-muted">→</span>

      <input
        id="filter-end"
        type="datetime-local"
        value={filters.end}
        onChange={(e) => onChange({ ...filters, end: e.target.value })}
        className={cn(inputClass, 'w-48')}
      />

      <label className="inline-flex items-center gap-1.5 text-sm text-text-secondary">
        <input
          type="checkbox"
          checked={filters.onlyErr}
          onChange={(e) => onChange({ ...filters, onlyErr: e.target.checked })}
          className="h-4 w-4 cursor-pointer rounded border-border text-accent transition-colors focus:ring-1 focus:ring-accent/20"
        />
        仅错误
      </label>

      {hasFilter && (
        <button
          type="button"
          onClick={onReset}
          className="text-sm text-muted transition-colors hover:text-accent"
        >
          清空
        </button>
      )}

      {trailing && <div className="ml-auto flex items-center gap-3">{trailing}</div>}
    </div>
  )
}
