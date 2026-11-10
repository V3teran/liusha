import { memo } from 'react'
import type { LogEntry } from '@/api/types'
import { cn } from '@/lib/utils'

// ── 展示辅助 ─────────────────────────────────────────────────────────────

const LEVEL_BADGE: Record<string, string> = {
  error: 'bg-sev-critical/15 text-sev-critical',
  warn: 'bg-sev-high/15 text-sev-high',
  warning: 'bg-sev-high/15 text-sev-high',
  info: 'bg-status-info/15 text-status-info',
  debug: 'bg-surface-3 text-text-tertiary',
}

function badgeClass(level: string): string {
  return LEVEL_BADGE[level] ?? 'bg-surface-3 text-text-tertiary'
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number, w = 2) => String(n).padStart(w, '0')
  const hms = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
  const now = new Date()
  const sameDay =
    d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate()
  return sameDay ? hms : `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hms}`
}

/** pid@host → pid@短主机（行内紧凑展示；完整值走 title）。 */
export function shortInstance(instance: string): string {
  const at = instance.indexOf('@')
  if (at < 0) return instance
  return `${instance.slice(0, at)}@${instance.slice(at + 1).split('.')[0]}`
}

/** 行详情内容：消息全文 + error 值 + 业务扩展字段。 */
function rowDetail(e: LogEntry): Array<{ label: string; value: string; tone?: 'error' }> {
  const out: Array<{ label: string; value: string; tone?: 'error' }> = [
    { label: 'message', value: e.message },
  ]
  if (e.error) out.push({ label: 'error', value: e.error, tone: 'error' })
  for (const [k, v] of Object.entries(e.fields ?? {})) {
    out.push({ label: k, value: typeof v === 'string' ? v : JSON.stringify(v) })
  }
  return out
}

// ── 行内摘要合成 ─────────────────────────────────────────────────────────
// message 往往是结构化日志的占位符（"request"/"Connect"），信息量在字段里。
// 列表行按形状还原一行可读文本（业界日志查看器的通行做法），全量字段仍走展开。

const SUMMARY_MAX = 200

function summaryText(v: unknown): string {
  // JSON.stringify(undefined) 返回 undefined 值——空缺字段兜底为空串
  const s = typeof v === 'string' ? v : (JSON.stringify(v) ?? '')
  return s.length > SUMMARY_MAX ? s.slice(0, SUMMARY_MAX) + '…' : s
}

function entrySummary(e: LogEntry): string {
  const f = e.fields ?? {}
  // 1. HTTP 访问形状：方法 路径 状态 · 耗时 · 客户端
  if (typeof f.method === 'string' && typeof f.path === 'string') {
    const status = typeof f.status === 'number' ? ` ${f.status}` : ''
    const latency = typeof f.latency_ms === 'number' ? ` · ${f.latency_ms}ms` : ''
    const client = typeof f.client === 'string' ? ` · ${f.client}` : ''
    return `${f.method} ${f.path}${status}${latency}${client}`
  }
  // 2. 错误文本是一等公民：message 作为上下文前缀（"xxx failed: connect refused"）
  const errText = e.error || summaryText(f.err) || summaryText(f.error) || ''
  if (errText) {
    const cause = errText.replace(/\s+/g, ' ')
    return e.message && e.message !== cause ? `${e.message}: ${cause}` : cause
  }
  // 3. 其余业务字段 k=v 紧凑拼接
  const keys = Object.keys(f)
  if (keys.length > 0) {
    return keys.map((k) => `${k}=${summaryText(f[k])}`).join(' ')
  }
  return e.message
}

// ── 单行日志 ─────────────────────────────────────────────────────────────

// memo：3s 增量轮询会新建 byService Map 触发整表 diff，行内容未变的行靠
// 引用稳定（entry 对象只增不改 + 原始值 props）跳过重渲染。
export const LogRow = memo(function LogRow({
  e,
  open,
  onToggle,
}: {
  e: LogEntry
  open: boolean
  onToggle: (key: string) => void
}) {
  const key = `${e.service}:${e.offset}`
  const summary = entrySummary(e)
  return (
    <>
      <div
        role="button"
        tabIndex={0}
        aria-expanded={open}
        aria-label={`日志：${summary}`}
        title="点击展开详情"
        onClick={() => onToggle(key)}
        onKeyDown={(ev) => {
          if (ev.key === 'Enter' || ev.key === ' ') {
            ev.preventDefault()
            onToggle(key)
          }
        }}
        className={cn(
          'flex cursor-pointer items-baseline gap-2.5 whitespace-nowrap px-4 py-[3px] font-mono text-[12.5px] leading-relaxed',
          e.level === 'error' ? 'bg-sev-critical/6 hover:bg-sev-critical/10' : 'hover:bg-surface-2',
        )}
      >
        <span className="shrink-0 text-text-tertiary" title={e.time}>
          {fmtTime(e.time)}
        </span>
        <span
          className={cn(
            'w-11 shrink-0 rounded px-0 text-center text-[10.5px] font-bold leading-5',
            badgeClass(e.level),
          )}
        >
          {e.level}
        </span>
        <span className="shrink-0 text-text-secondary" title={e.instance}>
          [<span className="font-semibold text-accent">{e.service}</span>
          {' · '}
          <span className="text-text-tertiary">{shortInstance(e.instance)}</span>]
        </span>
        <span className="min-w-0 flex-1 truncate text-text" title={summary}>
          {summary}
        </span>
        {e.caller && <span className="shrink-0 text-[11px] text-text-tertiary">{e.caller}</span>}
      </div>
      {open && (
        <div
          className={cn(
            'mb-1.5 ml-[92px] mr-4 whitespace-pre-wrap break-all rounded-lg bg-surface-2 px-3 py-2 font-mono text-xs text-text-secondary',
            e.level === 'error' ? 'border-l-2 border-sev-critical' : 'border-l-2 border-border-strong',
          )}
        >
          {rowDetail(e).map(({ label, value, tone }) => (
            <div key={label} className={tone === 'error' ? 'text-sev-critical' : undefined}>
              {label}: {value}
            </div>
          ))}
        </div>
      )}
    </>
  )
})
