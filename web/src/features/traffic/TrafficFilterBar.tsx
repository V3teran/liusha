import type { ReactNode } from 'react'
import type { TrafficFilters } from '@/api/types'

interface TrafficFilterBarProps {
  filters: TrafficFilters
  contentTypes: string[]
  onChange: (next: TrafficFilters) => void
  onReset: () => void
  trailing?: ReactNode // 右上尾随槽：总数 + 分页翻页，与各筛选框同处一行（ml-auto 推到最右）
}

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

// 状态大类：单字符映射后端 status_min/max 区间（见 client.applyStatusClass）。
const STATUS_CLASSES: Array<{ value: string; label: string }> = [
  { value: '2', label: '2xx 成功' },
  { value: '3', label: '3xx 跳转' },
  { value: '4', label: '4xx 客户端' },
  { value: '5', label: '5xx 服务端' },
]

// 统一控件高度（h-8）与圆角，聚焦时描边转 accent 并加一圈柔光——对齐 DevTools/Postman 过滤栏的密集但可读风格。
const CONTROL = 'h-8 rounded-md border border-border bg-surface text-sm text-text outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent-soft'
const SELECT_CLASS = `${CONTROL} px-2`
const INPUT_CLASS = `${CONTROL} px-2.5`

// datetime-local（本地 'YYYY-MM-DDTHH:mm'）↔ RFC3339（UTC，后端口径）互转。
// 空串保持空串（不筛该端）；非法输入解析失败时回退空串。
function localToIso(local: string): string {
  if (!local) return ''
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}
function isoToLocal(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  // 转成本地时区的 'YYYY-MM-DDTHH:mm'（datetime-local 只认无时区的本地串）。
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// 筛选栏：content-type / 方法 / 状态大类 / 搜索(host+url glob) / 时间区间。纯展示 + onChange 回调，
// 状态由页面层（URL）持有。下拉候选来自服务端 facet，搜索框支持 '*' 通配。trailing 槽承载总数 + 分页。
export function TrafficFilterBar({ filters, contentTypes, onChange, onReset, trailing }: TrafficFilterBarProps) {
  const hasFilter = !!(
    filters.method ||
    filters.contentType ||
    filters.statusClass ||
    filters.search ||
    filters.since ||
    filters.until
  )

  return (
    <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
      <select
        value={filters.contentType}
        onChange={(e) => onChange({ ...filters, contentType: e.target.value })}
        className={`w-[210px] ${SELECT_CLASS}`}
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
        className={`w-[116px] ${SELECT_CLASS}`}
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
        className={`w-[130px] ${SELECT_CLASS}`}
        aria-label="按状态大类筛选"
      >
        <option value="">全部状态</option>
        {STATUS_CLASSES.map((s) => (
          <option key={s.value} value={s.value}>
            {s.label}
          </option>
        ))}
      </select>

      {/* 搜索：跨 host + url 通配（'*'），前置放大镜暗示搜索语义。 */}
      <div className="relative">
        <svg
          aria-hidden
          viewBox="0 0 16 16"
          className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint"
        >
          <circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          <line x1="10.5" y1="10.5" x2="14" y2="14" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
        <input
          type="text"
          value={filters.search}
          onChange={(e) => onChange({ ...filters, search: e.target.value })}
          placeholder="搜索主机、URL（* 通配）"
          className={`w-[350px] pl-8 ${INPUT_CLASS}`}
          aria-label="搜索主机或 URL（支持 * 通配）"
        />
      </div>

      {/* 时间范围：起止两个 datetime-local（本地时区输入，回写 RFC3339）。 */}
      <div className="flex items-center gap-1.5">
        <input
          type="datetime-local"
          value={isoToLocal(filters.since)}
          onChange={(e) => onChange({ ...filters, since: localToIso(e.target.value) })}
          className={`w-[188px] ${INPUT_CLASS}`}
          aria-label="起始时间"
        />
        <span className="text-[12px] text-faint">→</span>
        <input
          type="datetime-local"
          value={isoToLocal(filters.until)}
          onChange={(e) => onChange({ ...filters, until: localToIso(e.target.value) })}
          className={`w-[188px] ${INPUT_CLASS}`}
          aria-label="结束时间"
        />
      </div>

      {hasFilter && (
        <button
          type="button"
          onClick={onReset}
          className="inline-flex h-8 items-center rounded-md px-2.5 text-[12.5px] text-muted transition-colors hover:bg-surface-2 hover:text-text"
        >
          清空筛选
        </button>
      )}

      {trailing && <div className="ml-auto flex items-center gap-3">{trailing}</div>}
    </div>
  )
}
