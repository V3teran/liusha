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
        setByService((prev) => {
          const next = new Map(prev)
          for (const name of res.reset) next.delete(name) // 轮转：该进程整段重取
          for (const e of res.entries) {
            const bucket = next.get(e.service) ?? []
            bucket.push(e)
            if (bucket.length > MAX_ENTRIES) bucket.splice(0, bucket.length - MAX_ENTRIES)
            next.set(e.service, bucket)
          }
          return next
        })
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

    void poll()
    if (!live) return () => { cancelled = true }
    const timer = setInterval(() => {
      if (document.hidden) return
      void poll()
    }, POLL_MS)
    return () => {
      cancelled = true
      clearInterval(timer)
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
