import { useEffect, useRef, useState } from 'react'
import { readLogs } from '@/api/logs'
import type { LogEntry, LogFilters } from '@/api/types'

// 日志流取数 hook：冷读尾窗口 + live 时 3s 游标增量轮询，按进程分桶累积、
// 上限 MAX_ENTRIES（超出丢最旧）。
//
// 不走 React Query：这是「有状态累积」（游标推进、按进程分桶、轮转重取）而非
// 「无状态快照」，塞进 RQ 的缓存模型反而拧巴。页面隐藏时跳过轮询（回前台下轮
// 立即补上），live=false 时完全停拉（静态快照，重开立即补一轮增量）。
export const MAX_ENTRIES = 1000

// 首屏每进程只取最新 300 条：打开即用（Live Tail 工具的通用首屏量级），
// 冷读延迟 ∝ 解析行数，300 条把打开路径压到几十毫秒；增量轮询仍按
// MAX_ENTRIES 给突发余量，已加载的更早日志不因首屏限量丢失语义
// （truncated 提示窗口外还有）。
const INITIAL_LIMIT = 300

const POLL_MS = 3000

export interface LogStream {
  /** 按进程分桶的已加载日志（桶内 = 文件序 = 时间序） */
  byService: Map<string, LogEntry[]>
  /** 冷读只扫到尾窗口/超限丢旧行的进程（「窗口外更早未加载」提示用） */
  truncated: Set<string>
  error: string | null
  /** 仅冷读进行中（增量轮询静默，避免 3s 一次的 loading 闪跳） */
  loading: boolean
  /** 整段重取（错误重试/手动刷新）：清游标与累积回到冷读 */
  refresh: () => void
}

export function useLogStream(filters: LogFilters, live: boolean): LogStream {
  const [byService, setByService] = useState<Map<string, LogEntry[]>>(() => new Map())
  const [truncated, setTruncated] = useState<Set<string>>(() => new Set())
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [tick, setTick] = useState(0)

  // 游标与请求代际放 ref：轮询闭包读最新值，且不触发重渲染。
  const cursorsRef = useRef<Record<string, number> | null>(null)
  const reqIdRef = useRef(0)

  // 筛选变化：清空累积回到冷读（filters 身份随 URL params 稳定，只在提交时变化）
  useEffect(() => {
    cursorsRef.current = null
    setByService(new Map())
    setTruncated(new Set())
    setError(null)
    setLoading(true)
  }, [filters])

  useEffect(() => {
    let cancelled = false
    const myReq = ++reqIdRef.current

    const poll = async () => {
      try {
        // 冷读（游标空）用首屏限量；增量轮询给满额余量抗突发
        const limit = cursorsRef.current ? MAX_ENTRIES : INITIAL_LIMIT
        const res = await readLogs(filters, cursorsRef.current, limit)
        if (cancelled || myReq !== reqIdRef.current) return
        cursorsRef.current = res.cursors
        setByService((prev) => mergeEntries(prev, res.reset, res.entries))
        setTruncated(new Set(res.truncated))
        setError(null)
      } catch (err) {
        if (!cancelled && myReq === reqIdRef.current) {
          setError(err instanceof Error ? err.message : '加载失败')
        }
      } finally {
        if (!cancelled && myReq === reqIdRef.current) setLoading(false)
      }
    }

    // 串行轮询：上一次请求落定后才调度下一次。固定间隔 setInterval 在请求慢于周期时会
    // 让两个请求携带同一游标并发，返回后重复追加日志、乱序响应还会让游标倒退。
    // 首拍无条件拉取（冷读）；后续页面隐藏时跳过本拍、保持调度。
    let timer: ReturnType<typeof setTimeout> | undefined
    const loop = async (first: boolean) => {
      if (first || !document.hidden) await poll()
      if (!cancelled && live) timer = setTimeout(() => void loop(false), POLL_MS)
    }
    void loop(true)
    return () => {
      cancelled = true
      if (timer !== undefined) clearTimeout(timer)
    }
  }, [filters, live, tick])

  const refresh = () => {
    cursorsRef.current = null
    setByService(new Map())
    setTruncated(new Set())
    setLoading(true)
    setTick((t) => t + 1)
  }

  return { byService, truncated, error, loading, refresh }
}

// mergeEntries 把一批增量并入按进程分桶的累积（纯函数：不改 prev 及其数组——React 可能对同一
// prev 重放更新函数，原地 push 会把同一批日志追加两次）。reset 中的进程先整段丢弃（日志轮转后
// 整段重取）；每桶超 MAX_ENTRIES 丢最旧。
function mergeEntries(
  prev: ReadonlyMap<string, LogEntry[]>,
  reset: readonly string[],
  entries: readonly LogEntry[],
): Map<string, LogEntry[]> {
  const next = new Map(prev)
  for (const name of reset) next.delete(name)
  const incoming = new Map<string, LogEntry[]>()
  for (const e of entries) {
    const bucket = incoming.get(e.service) // 本函数新建的局部数组，可就地追加
    if (bucket) bucket.push(e)
    else incoming.set(e.service, [e])
  }
  for (const [service, added] of incoming) {
    const merged = [...(next.get(service) ?? []), ...added]
    next.set(service, merged.length > MAX_ENTRIES ? merged.slice(merged.length - MAX_ENTRIES) : merged)
  }
  return next
}
