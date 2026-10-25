import type { LLMInvocationFilters } from '@/api/types'
import { cn } from '@/lib/utils'

interface LlmAuditFilterBarProps {
  filters: LLMInvocationFilters
  roles: string[]
  models: string[]
  onChange: (next: LLMInvocationFilters) => void
  onReset: () => void
  trailing?: React.ReactNode
}

/**
 * LlmAuditFilterBar - LLM 审计筛选栏
 *
 * 业界最佳实践（参考 Datadog/Grafana）：
 * 1. 日期范围有明确标签和占位符
 * 2. 统一输入框样式和间距
 * 3. 筛选控件左对齐，操作按钮右对齐
 */
export function LlmAuditFilterBar({ filters, roles, models, onChange, onReset, trailing }: LlmAuditFilterBarProps) {
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
