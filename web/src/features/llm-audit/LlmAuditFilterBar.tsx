import type { ReactNode } from 'react'
import type { LLMInvocationFilters } from '@/api/types'
import { FilterBar } from '@/components/FilterBar'
import { Select } from '@/components/ui/Select'
import { DateTimeRange } from '@/components/ui/DateTimeRange'
import { LlmAuditSearchBar } from './LlmAuditSearchBar'

interface LlmAuditFilterBarProps {
  filters: LLMInvocationFilters
  roles: string[]
  models: string[]
  search: string
  onSearchChange: (value: string) => void
  onChange: (next: LLMInvocationFilters) => void
  onReset: () => void
  trailing?: ReactNode
}

/**
 * LlmAuditFilterBar - LLM 审计筛选栏
 *
 * 全站统一筛选顺序（对齐 TrafficFilterBar/TaskFilterBar/漏洞页）：
 * 枚举下拉（粗→细：角色→模型）→ 关键词搜索 → 时间范围 → 布尔开关 → 清空 →（最右）分页。
 * 搜索框自带 300ms 防抖（LlmAuditSearchBar 内置）。时间范围在 URL 中保存
 * datetime-local 原值，由 API 层（api/llmaudit.ts）发送前换算为 ISO。
 */
export function LlmAuditFilterBar({ filters, roles, models, search, onSearchChange, onChange, onReset, trailing }: LlmAuditFilterBarProps) {
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end || search)

  return (
    <FilterBar label="LLM 调用筛选" canReset={hasFilter} onReset={onReset} trailing={trailing}>
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

      <LlmAuditSearchBar value={search} onChange={onSearchChange} />

      <DateTimeRange
        start={filters.start}
        end={filters.end}
        onChange={({ start, end }) => onChange({ ...filters, start, end })}
        startLabel="调用时间起"
        endLabel="调用时间止"
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
