import type { LLMInvocationFilters } from '@/api/types'

interface LlmAuditFilterBarProps {
  filters: LLMInvocationFilters
  roles: string[]
  models: string[]
  onChange: (next: LLMInvocationFilters) => void
  onReset: () => void
  trailing?: React.ReactNode // 右侧附加信息（如「第 N 页 · 本页 N 条」）
}

// 筛选栏：角色 / 模型 / 时间范围 / 仅错误。纯展示 + onChange 回调，状态由页面层（URL）持有。
export function LlmAuditFilterBar({ filters, roles, models, onChange, onReset, trailing }: LlmAuditFilterBarProps) {
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end)

  return (
    <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
      <select
        value={filters.role}
        onChange={(e) => onChange({ ...filters, role: e.target.value })}
        className="w-[156px] rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
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
        className="w-[170px] rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
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
        className="rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
      />
      <span className="text-muted">→</span>
      <input
        type="datetime-local"
        value={filters.end}
        onChange={(e) => onChange({ ...filters, end: e.target.value })}
        className="rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
      />
      <label className="inline-flex items-center gap-1.5 text-[12.5px] text-muted">
        <input
          type="checkbox"
          checked={filters.onlyErr}
          onChange={(e) => onChange({ ...filters, onlyErr: e.target.checked })}
        />
        仅错误
      </label>
      {hasFilter && (
        <button type="button" onClick={onReset} className="text-[12.5px] text-muted hover:text-accent">
          清空筛选
        </button>
      )}
      {trailing && <span className="ml-auto text-[12.5px] text-muted tabular-nums">{trailing}</span>}
    </div>
  )
}
