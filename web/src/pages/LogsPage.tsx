import { SearchInput } from '@/components/SearchInput'
import { FilterBar } from '@/components/FilterBar'
import { Select } from '@/components/ui/Select'
import { EmptyState } from '@/components/ui/EmptyState'
import { PageHeader } from '@/components/PageHeader'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ArrowDown, RotateCw } from 'lucide-react'
import { listLogProcesses } from '@/api/logs'
import { cn } from '@/lib/utils'
import { useLogFilters } from '@/features/logs/useLogFilters'
import { useLogStream } from '@/features/logs/useLogStream'
import { LogRow, shortInstance } from '@/features/logs/LogRow'

// 日志页：各进程日志的统一视图（logs/{process}.log 尾读；进程名即 liusha 子命令名）。
//
// 交互（Live Tail 范式）：实时跟随默认开（3s 增量轮询，脉冲圆点）；向上滚动即
// 暂停自动滚底并浮出「有新日志」回底条，滚回底部/点击恢复跟随。所有行可展开
// 详情（消息全文 + error + 业务字段）——统一心智：截断行不依赖 hover title
// （触屏无 hover），任意行都有全文出口。筛选态进 URL（useLogFilters）。
export function LogsPage() {
  const { filters, setFilters } = useLogFilters()
  const [live, setLive] = useState(true)
  const stream = useLogStream(filters, live)

  const { data: procData, isSuccess: processesLoaded } = useQuery({
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
      if (localSearch !== filters.search) setFilters({ ...filters, search: localSearch }, { replace: true })
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
  const toggleRow = useCallback((key: string) => {
    setOpenKey((prev) => (prev === key ? null : key))
  }, [])

  // 没有任何日志文件：文件日志没开，或数据目录不对
  const noLogFiles = processesLoaded && processes.length === 0
  // 进程选项 = 有日志文件的进程；URL 里带着的进程若已无文件（旧链接）也保留，选择框如实显示当前筛选
  const processOptions =
    filters.process && !processes.some((p) => p.name === filters.process)
      ? [...processes.map((p) => p.name), filters.process]
      : processes.map((p) => p.name)
  const hasFilter = !!(filters.process || filters.instance || filters.level || filters.search)
  const resetFilters = () => setFilters({ process: '', instance: '', level: '', search: '' })

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="服务器日志">
        <button
          type="button"
          onClick={() => setLive((v) => !v)}
          aria-pressed={live}
          className={cn(
            'inline-flex h-9 shrink-0 items-center gap-2 rounded-lg border px-3 text-sm font-medium transition-colors',
            live
              ? 'border-status-success/45 bg-status-success/8 text-status-success'
              : 'border-border bg-surface text-text-secondary hover:text-text',
          )}
        >
          <span className={cn('h-1.5 w-1.5 rounded-full bg-current', live && 'animate-pulse')} />
          {live ? '实时跟随中' : '已暂停'}
        </button>
      </PageHeader>

      {/* 筛选栏（全站统一顺序）：进程 → 实例 → 级别 → 搜索（防抖）→ 清空 →（右）计数 */}
      <FilterBar
        label="日志筛选"
        canReset={hasFilter}
        onReset={resetFilters}
        trailing={
          <div className="font-mono text-xs text-text-tertiary">
            {stream.error ? (
              <button
                type="button"
                onClick={stream.refresh}
                className="inline-flex items-center gap-1 text-status-error hover:underline"
              >
                <RotateCw className="h-3 w-3" /> {stream.error} · 重试
              </button>
            ) : (
              <span>
                {stream.loading && entries.length === 0 ? '加载中…' : `共 ${entries.length} 条`}
                {stream.truncated.size > 0 && ' · 窗口外更早未加载'}
                {!live && ' · 已暂停'}
              </span>
            )}
          </div>
        }
      >
        <Select
          value={filters.process}
          onChange={(e) => setFilters({ ...filters, process: e.target.value, instance: '' })}
          className="w-36"
          aria-label="按进程筛选"
        >
          <option value="">全部进程</option>
          {processOptions.map((name) => (
            <option key={name} value={name}>
              {name}
            </option>
          ))}
        </Select>

        <Select
          value={filters.instance}
          onChange={(e) => setFilters({ ...filters, instance: e.target.value })}
          className="w-52"
          aria-label="按实例筛选"
          disabled={instanceOptions.length === 0}
        >
          <option value="">全部实例</option>
          {instanceOptions.map((o) => (
            <option key={`${o.label}-${o.value}`} value={o.value}>
              {o.label}
            </option>
          ))}
        </Select>

        <Select
          value={filters.level}
          onChange={(e) => setFilters({ ...filters, level: e.target.value })}
          className="w-32"
          aria-label="按级别筛选"
        >
          <option value="">全部级别</option>
          <option value="error">error</option>
          <option value="warn">warn</option>
          <option value="info">info</option>
          <option value="debug">debug</option>
        </Select>

        <SearchInput
          className="w-64"
          value={localSearch}
          onChange={setLocalSearch}
          placeholder="搜索消息、error、业务字段…"
          ariaLabel="搜索日志"
        />
      </FilterBar>

      <div className="relative min-h-0 flex-1">
        <div ref={listRef} onScroll={onScroll} className="h-full overflow-y-auto py-1">
          {!stream.loading && entries.length === 0 ? (
            noLogFiles ? (
              <EmptyState
                kind="empty"
                noun="日志"
                hint="日志写在数据目录的 logs/ 下，确认 config.yaml 的 log.to_file 已开启。"
                className="font-sans"
              />
            ) : hasFilter ? (
              <EmptyState kind="no-match" noun="日志" className="font-sans" />
            ) : (
              <EmptyState kind="empty" noun="日志" hint="各组件运行后，日志会实时出现在这里。" className="font-sans" />
            )
          ) : (
            entries.map((e) => (
              <LogRow
                key={`${e.service}:${e.offset}`}
                e={e}
                open={openKey === `${e.service}:${e.offset}`}
                onToggle={toggleRow}
              />
            ))
          )}
        </div>

        {live && !follow && pending > 0 && (
          <button
            type="button"
            onClick={followNow}
            className="absolute inset-x-0 bottom-4 mx-auto flex w-fit items-center gap-1.5 rounded-full border border-accent/30 bg-accent-soft px-3.5 py-1.5 text-xs text-accent shadow-md transition-colors hover:bg-accent/15"
          >
            <ArrowDown aria-hidden className="h-3.5 w-3.5" />
            {pending} 条新日志，回到底部跟随
          </button>
        )}
      </div>
    </div>
  )
}
