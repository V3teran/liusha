import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { LogFilters } from '@/api/types'
import type { HistoryOptions } from '@/hooks/useSelectedParam'

// 日志筛选态进 URL（对齐 useTaskFilters「筛选应作为 URL state」）：
// 刷新/后退/分享链接都能还原。实时跟随开关是易失会话态，不进 URL。
const EMPTY: LogFilters = { process: '', instance: '', level: '', search: '' }

export function useLogFilters() {
  const [searchParams, setSearchParams] = useSearchParams()

  const filters = useMemo<LogFilters>(
    () => ({
      process: searchParams.get('process') ?? '',
      instance: searchParams.get('instance') ?? '',
      level: searchParams.get('level') ?? '',
      search: searchParams.get('search') ?? '',
    }),
    [searchParams],
  )

  // 历史：选项改动新增一条；搜索框输入传 replace（打字不是导航）。
  const setFilters = (next: LogFilters, opts?: HistoryOptions) =>
    setSearchParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        const setOrDel = (key: string, val: string) => (val ? p.set(key, val) : p.delete(key))
        setOrDel('process', next.process)
        setOrDel('instance', next.instance)
        setOrDel('level', next.level)
        setOrDel('search', next.search)
        return p
      },
      { replace: opts?.replace },
    )

  const resetFilters = () => setFilters(EMPTY)

  return { filters, setFilters, resetFilters }
}
