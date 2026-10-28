import { type ReactNode, useEffect, useState } from 'react'
import { Search } from 'lucide-react'
import type { TrafficFilters } from '@/api/types'
import { cn } from '@/lib/utils'

interface TrafficFilterBarProps {
  filters: TrafficFilters
  contentTypes: string[]
  onChange: (next: TrafficFilters) => void
  onReset: () => void
  trailing?: ReactNode
}

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

const STATUS_CLASSES: Array<{ value: string; label: string }> = [
  { value: '2', label: '2xx 成功' },
  { value: '3', label: '3xx 跳转' },
  { value: '4', label: '4xx 客户端' },
  { value: '5', label: '5xx 服务端' },
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

/**
 * TrafficFilterBar - 流量筛选栏
 */
export function TrafficFilterBar({ filters, contentTypes, onChange, onReset, trailing }: TrafficFilterBarProps) {
  // 搜索框本地态 + 300ms 防抖：search 每键击直写 URL 会连环发请求（流量表是全系统增长最快的表），
  // 对齐 LlmAuditSearchBar 的防抖口径。外部变化（清空筛选/回退）即时回填本地态。
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

  const hasFilter = !!(
    filters.method ||
    filters.contentType ||
    filters.statusClass ||
    filters.search ||
    filters.since ||
    filters.until
  )

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
    <div className="-mx-6 mb-3 flex flex-wrap items-center gap-3 border-b border-border bg-surface/30 px-6 pb-3">
      <select
        value={filters.contentType}
        onChange={(e) => onChange({ ...filters, contentType: e.target.value })}
        className={cn(selectClass, 'w-52')}
        aria-label="按 Content-Type 筛选"
      >
        <option value="">全部类型</option>
        {contentTypes.map((ct) => (
          <option key={ct} value={ct}>
            {ct}
          </option>
        ))}
      </select>

      <select
        value={filters.method}
        onChange={(e) => onChange({ ...filters, method: e.target.value })}
        className={cn(selectClass, 'w-32')}
        aria-label="按方法筛选"
      >
        <option value="">全部方法</option>
        {METHODS.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>

      <select
        value={filters.statusClass}
        onChange={(e) => onChange({ ...filters, statusClass: e.target.value })}
        className={cn(selectClass, 'w-36')}
        aria-label="按状态大类筛选"
      >
        <option value="">全部状态</option>
        {STATUS_CLASSES.map((s) => (
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
          placeholder="搜索主机、URL（* 通配）"
          className={cn(inputClass, 'w-80 pl-10')}
          aria-label="搜索主机或 URL（支持 * 通配）"
        />
      </div>

      <div className="flex items-center gap-2">
        <input
          type="datetime-local"
          value={isoToLocal(filters.since)}
          onChange={(e) => onChange({ ...filters, since: localToIso(e.target.value) })}
          className={cn(inputClass, 'w-48')}
          aria-label="起始时间"
        />
        <span className="text-sm text-text-tertiary">→</span>
        <input
          type="datetime-local"
          value={isoToLocal(filters.until)}
          onChange={(e) => onChange({ ...filters, until: localToIso(e.target.value) })}
          className={cn(inputClass, 'w-48')}
          aria-label="结束时间"
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
