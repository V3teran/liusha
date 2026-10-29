import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { RotateCw, Search } from 'lucide-react'
import { listLogProcesses } from '@/api/client'
import type { LogEntry } from '@/api/types'
import { cn } from '@/lib/utils'
import { useLogFilters } from './useLogFilters'
import { useLogStream } from './useLogStream'

// 日志页：api/proxy/runner 三进程日志的统一视图（logs/{process}.log 尾读）。
//
// 交互（Datadog Live Tail 范式）：实时跟随默认开（3s 增量轮询，脉冲圆点）；
// 向上滚动即暂停自动滚底并浮出「有新日志」回底条，滚回底部/点击恢复跟随。
// error 或带业务字段（task_id 等）的行可展开详情。筛选态进 URL（useLogFilters）。

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
function shortInstance(instance: string): string {
  const at = instance.indexOf('@')
  if (at < 0) return instance
  return `${instance.slice(0, at)}@${instance.slice(at + 1).split('.')[0]}`
}

/** 行是否有可展开详情：error 值或业务扩展字段。 */
function hasDetail(e: LogEntry): boolean {
  return !!e.error || !!e.fields && Object.keys(e.fields).length > 0
}

// ── 单行日志 ─────────────────────────────────────────────────────────────

function LogRow({ e, open, onToggle }: { e: LogEntry; open: boolean; onToggle: () => void }) {
  const detail = hasDetail(e)
  return (
    <>
      <div
        role={detail ? 'button' : undefined}
        tabIndex={detail ? 0 : undefined}
        aria-expanded={detail ? open : undefined}
        aria-label={`日志：${e.message}`}
        title={detail ? '点击展开详情' : undefined}
        onClick={detail ? onToggle : undefined}
        onKeyDown={
          detail
            ? (ev) => {
                if (ev.key === 'Enter' || ev.key === ' ') {
                  ev.preventDefault()
                  onToggle()
                }
              }
            : undefined
        }
        className={cn(
          'flex items-baseline gap-2.5 whitespace-nowrap px-4 py-[3px] font-mono text-[12.5px] leading-relaxed',
          detail && 'cursor-pointer',
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
        <span className="min-w-0 flex-1 truncate text-text" title={e.message}>
          {e.message}
        </span>
        {e.caller && <span className="shrink-0 text-[11px] text-text-tertiary">{e.caller}</span>}
      </div>
      {detail && open && (
        <div
          className={cn(
            'mb-1.5 ml-[92px] mr-4 whitespace-pre-wrap break-all rounded-lg bg-surface-2 px-3 py-2 font-mono text-xs text-text-secondary',
            e.level === 'error' ? 'border-l-2 border-sev-critical' : 'border-l-2 border-border-strong',
          )}
        >
          {e.error && <div className="text-sev-critical">error: {e.error}</div>}
          {Object.entries(e.fields ?? {}).map(([k, v]) => (
            <div key={k}>
              {k}: {typeof v === 'string' ? v : JSON.stringify(v)}
            </div>
          ))}
        </div>
      )}
    </>
  )
}

// ── 页面 ─────────────────────────────────────────────────────────────────

export function LogsPage() {
  const { filters, setFilters } = useLogFilters()
  const [live, setLive] = useState(true)
  const stream = useLogStream(filters, live)

  const { data: procData } = useQuery({
    queryKey: ['log-processes'],
    queryFn: listLogProcesses,
    refetchInterval: 15000,
  })
  const processes = procData?.processes ?? []

  // 跨进程按时间归并（桶内已有序，归并即整体排序）
  const entries = useMemo(() => {
    const all = [...stream.byService.values()].flat()
    all.sort((a, b) => (a.time < b.time ? -1 : a.time > b.time ? 1 : 0))
    return all
  }, [stream.byService])

  // ── 跟随/暂停：贴底自动滚，上滚暂停并计新条数 ──────────────────────────
  const listRef = useRef<HTMLDivElement>(null)
  const [follow, setFollow] = useState(true)
  const [pending, setPending] = useState(0)
  const prevCountRef = useRef(0)

  useEffect(() => {
    // 筛选变化 → 视口重置为跟随
    prevCountRef.current = 0
    setPending(0)
    setFollow(true)
  }, [filters])

  useEffect(() => {
    const delta = entries.length - prevCountRef.current
    prevCountRef.current = entries.length
    if (delta <= 0) return
    if (follow && live) {
      const el = listRef.current
      el?.scrollTo({ top: el.scrollHeight })
      setPending(0)
    } else if (live) {
      setPending((p) => p + delta)
    }
  }, [entries, follow, live])

  const onScroll = useCallback(() => {
    const el = listRef.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 30
    setFollow(atBottom)
    if (atBottom) setPending(0)
  }, [])

  const followNow = () => {
    setFollow(true)
    setPending(0)
    const el = listRef.current
    el?.scrollTo({ top: el.scrollHeight })
  }

  // ── 筛选交互 ───────────────────────────────────────────────────────────
  const [localSearch, setLocalSearch] = useState(filters.search)
  useEffect(() => {
    setLocalSearch(filters.search)
  }, [filters.search])
  useEffect(() => {
    const timer = setTimeout(() => {
      if (localSearch !== filters.search) setFilters({ ...filters, search: localSearch })
    }, 300)
    return () => clearTimeout(timer)
  }, [localSearch, filters, setFilters])

  const instanceOptions = useMemo(() => {
    const out: Array<{ value: string; label: string }> = []
    for (const p of processes) {
      if (filters.process && p.name !== filters.process) continue
      for (const inst of p.instances) out.push({ value: inst, label: `${p.name} · ${shortInstance(inst)}` })
    }
    return out
  }, [processes, filters.process])

  const [openKey, setOpenKey] = useState<string | null>(null)
  const toggleRow = useCallback((e: LogEntry) => {
    const key = `${e.service}:${e.offset}`
    setOpenKey((prev) => (prev === key ? null : key))
  }, [])

  const anyExists = processes.some((p) => p.exists)
  const hasFilter = !!(filters.process || filters.instance || filters.level || filters.search)
  const controlClass = cn(
    'h-9 rounded-lg border border-border bg-surface px-3 text-sm text-text',
    'outline-none transition-all placeholder:text-text-tertiary',
    'focus:border-accent focus:glow-accent',
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div className="min-w-0">
          <h1 className="text-base font-semibold text-text">日志</h1>
          <p className="mt-0.5 text-sm text-text-secondary">api / proxy / runner 进程日志 · 文件直读 logs/ 目录</p>
        </div>
        <button
          type="button"
          onClick={() => setLive((v) => !v)}
          aria-pressed={live}
          className={cn(
            'inline-flex h-9 shrink-0 items-center gap-2 rounded-lg border px-3 text-[13px] font-medium transition-all',
            live
              ? 'border-status-success/45 bg-status-success/8 text-status-success'
              : 'border-border bg-surface text-text-secondary hover:text-text',
          )}
        >
          <span className={cn('h-1.5 w-1.5 rounded-full bg-current', live && 'animate-pulse')} />
          {live ? '实时跟随中' : '已暂停'}
        </button>
      </header>

      {/* 筛选栏（全站统一顺序）：进程 → 实例 → 级别 → 搜索（防抖）→（右）计数 */}
      <div className="flex flex-wrap items-center gap-3 border-b border-border bg-surface/30 px-6 py-3">
        <select
          value={filters.process}
          onChange={(e) => setFilters({ ...filters, process: e.target.value, instance: '' })}
          className={cn(controlClass, 'w-36')}
          aria-label="按进程筛选"
        >
          <option value="">全部进程</option>
          {processes.map((p) => (
            <option key={p.name} value={p.name}>
              {p.name}
              {!p.exists && '（无日志文件）'}
            </option>
          ))}
        </select>

        <select
          value={filters.instance}
          onChange={(e) => setFilters({ ...filters, instance: e.target.value })}
          className={cn(controlClass, 'w-52')}
          aria-label="按实例筛选"
          disabled={instanceOptions.length === 0}
        >
          <option value="">全部实例</option>
          {instanceOptions.map((o) => (
            <option key={`${o.label}-${o.value}`} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>

        <select
          value={filters.level}
          onChange={(e) => setFilters({ ...filters, level: e.target.value })}
          className={cn(controlClass, 'w-32')}
          aria-label="按级别筛选"
        >
          <option value="">全部级别</option>
          <option value="error">error</option>
          <option value="warn">warn</option>
          <option value="info">info</option>
          <option value="debug">debug</option>
        </select>

        <div className="relative">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-text-tertiary" />
          <input
            type="text"
            value={localSearch}
            onChange={(e) => setLocalSearch(e.target.value)}
            placeholder="搜索消息、error、业务字段…"
            className={cn(controlClass, 'w-72 pl-10')}
            aria-label="搜索日志"
          />
        </div>

        <div className="ml-auto flex items-center gap-3 font-mono text-xs text-text-tertiary">
          {stream.error ? (
            <button
              type="button"
              onClick={stream.refresh}
              className="inline-flex items-center gap-1 text-sev-critical hover:underline"
            >
              <RotateCw className="h-3 w-3" /> {stream.error} · 点击重试
            </button>
          ) : (
            <span>
              {stream.loading && entries.length === 0
                ? '加载中…'
                : `共 ${entries.length} 条`}
              {stream.truncated.size > 0 && ' · 窗口外更早未加载'}
              {!live && ' · 已暂停'}
            </span>
          )}
        </div>
      </div>

      <div className="relative min-h-0 flex-1">
        <div ref={listRef} onScroll={onScroll} className="h-full overflow-y-auto py-1">
          {!stream.loading && entries.length === 0 ? (
            <div className="p-12 text-center font-sans text-sm text-text-tertiary">
              {anyExists
                ? hasFilter
                  ? '当前筛选无匹配日志'
                  : '暂无日志输出'
                : '未发现日志文件——进程日志目录为 logs/（LIUSHA_LOG_DIR 可改），确认各进程已开启文件日志'}
            </div>
          ) : (
            entries.map((e) => (
              <LogRow
                key={`${e.service}:${e.offset}`}
                e={e}
                open={openKey === `${e.service}:${e.offset}`}
                onToggle={() => toggleRow(e)}
              />
            ))
          )}
        </div>

        {live && !follow && pending > 0 && (
          <button
            type="button"
            onClick={followNow}
            className="absolute bottom-4 left-1/2 -translate-x-1/2 rounded-full border border-accent/30 bg-accent-soft px-3.5 py-1.5 text-xs text-accent shadow-md transition-all hover:bg-accent/15"
          >
            ↓ 有新日志（{pending}）· 点击回到底部跟随
          </button>
        )}
      </div>
    </div>
  )
}
