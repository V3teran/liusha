import type { TrafficFilters } from '@/api/types'

interface TrafficFilterBarProps {
  filters: TrafficFilters
  hosts: string[]
  onChange: (next: TrafficFilters) => void
  onReset: () => void
  trailing?: React.ReactNode // 右侧附加信息（如「第 N 页 · 共 N 条」）
}

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

const INPUT_CLASS = 'rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent'

// 筛选栏：主机 / 方法 / 路径(glob) / 状态码范围。纯展示 + onChange 回调，状态由页面层（URL）持有。
// 与 LlmAuditFilterBar 同结构：下拉候选来自服务端 facet，path 支持 '*' 通配。
export function TrafficFilterBar({ filters, hosts, onChange, onReset, trailing }: TrafficFilterBarProps) {
  const hasFilter = !!(filters.host || filters.method || filters.path || filters.statusMin || filters.statusMax)

  // 状态码输入：空串→0（不限），非法值不写入。
  const setStatus = (key: 'statusMin' | 'statusMax', raw: string) => {
    const n = raw === '' ? 0 : Number(raw)
    if (!Number.isFinite(n) || n < 0) return
    onChange({ ...filters, [key]: n })
  }

  return (
    <div className="mb-3.5 flex flex-wrap items-center gap-2.5">
      <select
        value={filters.host}
        onChange={(e) => onChange({ ...filters, host: e.target.value })}
        className={`w-[190px] ${INPUT_CLASS}`}
        aria-label="按主机筛选"
      >
        <option value="">全部主机</option>
        {hosts.map((h) => (
          <option key={h} value={h}>
            {h}
          </option>
        ))}
      </select>
      <select
        value={filters.method}
        onChange={(e) => onChange({ ...filters, method: e.target.value })}
        className={`w-[116px] ${INPUT_CLASS}`}
        aria-label="按方法筛选"
      >
        <option value="">全部方法</option>
        {METHODS.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>
      <input
        type="text"
        value={filters.path}
        onChange={(e) => onChange({ ...filters, path: e.target.value })}
        placeholder="路径 /api/* 通配"
        className={`w-[200px] ${INPUT_CLASS}`}
        aria-label="按路径筛选（支持 * 通配）"
      />
      <span className="inline-flex items-center gap-1.5 text-[12.5px] text-muted">
        状态
        <input
          type="number"
          inputMode="numeric"
          min={0}
          value={filters.statusMin || ''}
          onChange={(e) => setStatus('statusMin', e.target.value)}
          placeholder="≥"
          className={`w-[66px] ${INPUT_CLASS}`}
          aria-label="状态码下界"
        />
        <span>–</span>
        <input
          type="number"
          inputMode="numeric"
          min={0}
          value={filters.statusMax || ''}
          onChange={(e) => setStatus('statusMax', e.target.value)}
          placeholder="≤"
          className={`w-[66px] ${INPUT_CLASS}`}
          aria-label="状态码上界"
        />
      </span>
      {hasFilter && (
        <button type="button" onClick={onReset} className="text-[12.5px] text-muted hover:text-accent">
          清空筛选
        </button>
      )}
      {trailing && <span className="ml-auto text-[12.5px] text-muted tabular-nums">{trailing}</span>}
    </div>
  )
}
