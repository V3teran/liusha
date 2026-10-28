import { type ReactNode, useEffect, useState } from 'react'
import { Search } from 'lucide-react'
import type { TaskFilters } from '@/api/types'
import { cn } from '@/lib/utils'

interface TaskFilterBarProps {
  filters: TaskFilters
  onChange: (next: TaskFilters) => void
  onReset: () => void
  trailing?: ReactNode
}

const SOURCES: Array<{ value: string; label: string }> = [
  { value: 'manual', label: '主动下发' },
  { value: 'auto', label: '被动代理' },
]

const STATUSES: Array<{ value: string; label: string }> = [
  { value: 'active', label: '运行中' },
  { value: 'completed', label: '已完成' },
  { value: 'aborted', label: '已中止' },
]

function localToIso(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

function isoToLocal(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

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

  const selectClass = cn(
    'h-9 rounded-lg border border-border bg-surface px-3 text-sm text-text',
    'outline-none transition-all focus:border-accent focus:glow-accent',
  )
  const inputClass = cn(
    'h-9 rounded-lg border border-border bg-surface px-3 text-sm text-text',
    'outline-none transition-all placeholder:text-text-tertiary',
    'focus:border-accent focus:glow-accent',
  )

  return (
    <div className="-mx-6 mb-3 flex flex-wrap items-center gap-3 border-b border-border bg-surface/30 px-6 pb-3">
      <select
        value={filters.source}
        onChange={(e) => onChange({ ...filters, source: e.target.value })}
        className={cn(selectClass, 'w-36')}
        aria-label="按来源筛选"
      >
        <option value="">全部来源</option>
        {SOURCES.map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </select>

      <select
        value={filters.status}
        onChange={(e) => onChange({ ...filters, status: e.target.value })}
        className={cn(selectClass, 'w-32')}
        aria-label="按状态筛选"
      >
        <option value="">全部状态</option>
        {STATUSES.map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </select>

      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
        <input
          type="text"
          value={localSearch}
          onChange={(e) => setLocalSearch(e.target.value)}
          placeholder="搜索目标、批次ID"
          className={cn(inputClass, 'w-72 pl-10')}
          aria-label="搜索目标或批次ID"
        />
      </div>

      <div className="flex items-center gap-2">
        <input
          type="datetime-local"
          value={isoToLocal(filters.since)}
          onChange={(e) => onChange({ ...filters, since: localToIso(e.target.value) })}
          className={cn(inputClass, 'w-48')}
          aria-label="创建时间起"
        />
        <span className="text-sm text-text-tertiary">→</span>
        <input
          type="datetime-local"
          value={isoToLocal(filters.until)}
          onChange={(e) => onChange({ ...filters, until: localToIso(e.target.value) })}
          className={cn(inputClass, 'w-48')}
          aria-label="创建时间止"
        />
      </div>

      {hasFilter && (
        <button
          type="button"
          onClick={onReset}
          className="h-9 rounded-lg px-3 text-sm text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
        >
          清空筛选
        </button>
      )}

      {trailing && <div className="ml-auto flex items-center gap-3">{trailing}</div>}
    </div>
  )
}
