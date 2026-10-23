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
 * 设计改进：
 * 1. 统一圆角和间距
 * 2. 聚焦辉光效果
 * 3. 更清晰的布局
 */
export function LlmAuditFilterBar({ filters, roles, models, onChange, onReset, trailing }: LlmAuditFilterBarProps) {
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end)

  const selectClass = cn(
    'h-9 rounded-lg border border-border bg-surface px-3 text-sm text-text',
    'outline-none transition-all focus:border-accent focus:glow-accent'
  )

  const inputClass = cn(
    'h-9 rounded-lg border border-border bg-surface px-3 text-sm text-text',
    'outline-none transition-all placeholder:text-text-tertiary',
    'focus:border-accent focus:glow-accent'
  )

  return (
    <div className="mb-4 flex flex-wrap items-center gap-3">
      <select
        value={filters.role}
        onChange={(e) => onChange({ ...filters, role: e.target.value })}
        className={cn(selectClass, 'w-40')}
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
        className={cn(selectClass, 'w-44')}
      >
        <option value="">全部模型</option>
        {models.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>

      <input
        type="datetime-local"
        value={filters.start}
        onChange={(e) => onChange({ ...filters, start: e.target.value })}
        className={inputClass}
      />

      <span className="text-sm text-text-tertiary">→</span>

      <input
        type="datetime-local"
        value={filters.end}
        onChange={(e) => onChange({ ...filters, end: e.target.value })}
        className={inputClass}
      />

      <label className="inline-flex items-center gap-2 text-sm text-text-secondary">
        <input
          type="checkbox"
          checked={filters.onlyErr}
          onChange={(e) => onChange({ ...filters, onlyErr: e.target.checked })}
          className="accent-accent"
        />
        仅错误
      </label>

      {hasFilter && (
        <button
          type="button"
          onClick={onReset}
          className="text-sm text-text-secondary transition-colors hover:text-accent"
        >
          清空筛选
        </button>
      )}

      {trailing && <div className="ml-auto flex items-center gap-3">{trailing}</div>}
    </div>
  )
}
