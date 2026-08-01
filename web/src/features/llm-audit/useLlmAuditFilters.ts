import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { LLMInvocationFilters } from '@/api/types'

// 筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/排序/分页应作为 URL state」）：
// 刷新页面、前进/后退、把链接发给同事复核，筛选条件与翻页位置都能还原，不再只存在组件内存里就丢失。
//
// 参数名：task / role / model / only_err / start / end / cursors。
// 用 task 而非 owner——后端早已把 owner_type/owner_id 多态坍缩为统一 task 表（commit a491e8a5），
// 路由也全是 /llm/invocations/:task_id，前端 URL 层不该再用历史上的 owner 叫法。
// cursors 是 keyset 游标栈（逗号分隔的 id 列表，如 "0,42,108"）——keyset 分页不能像 offset
// 那样任意跳页，「上一页」本质是弹栈回到上一个游标，故整个栈都需要保留而非只存当前游标。
const PARAM_KEYS = ['role', 'model', 'only_err', 'start', 'end'] as const

export interface LlmAuditUrlState {
  taskId: string
  filters: LLMInvocationFilters
  cursors: number[] // 栈顶（末项）是当前页起点 after 值；[0] 表示首页
  pageNo: number // 1-based，即 cursors.length
  setTaskId: (taskId: string) => void
  setFilters: (next: LLMInvocationFilters) => void
  goNextPage: (nextAfter: number) => void
  goPrevPage: () => void
  resetFilters: () => void
}

const EMPTY_FILTERS: LLMInvocationFilters = { role: '', model: '', onlyErr: false, start: '', end: '' }

function parseCursors(raw: string | null): number[] {
  if (!raw) return [0]
  const nums = raw
    .split(',')
    .map((s) => Number(s))
    .filter((n) => Number.isFinite(n) && n >= 0)
  return nums.length > 0 ? nums : [0]
}

export function useLlmAuditFilters(): LlmAuditUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const taskId = searchParams.get('task') ?? ''
  const cursors = useMemo(() => parseCursors(searchParams.get('cursors')), [searchParams])
  const filters = useMemo<LLMInvocationFilters>(
    () => ({
      role: searchParams.get('role') ?? '',
      model: searchParams.get('model') ?? '',
      onlyErr: searchParams.get('only_err') === '1',
      start: searchParams.get('start') ?? '',
      end: searchParams.get('end') ?? '',
    }),
    [searchParams],
  )

  const setTaskId = (nextTaskId: string) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (nextTaskId) next.set('task', nextTaskId)
      else next.delete('task')
      // 换 task：role/model/时间范围/游标是上一个会话的筛选态，不该带过去。
      PARAM_KEYS.forEach((k) => next.delete(k))
      next.delete('cursors')
      return next
    })
  }

  const setFilters = (nextFilters: LLMInvocationFilters) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (nextFilters.role) next.set('role', nextFilters.role)
      else next.delete('role')
      if (nextFilters.model) next.set('model', nextFilters.model)
      else next.delete('model')
      if (nextFilters.onlyErr) next.set('only_err', '1')
      else next.delete('only_err')
      if (nextFilters.start) next.set('start', nextFilters.start)
      else next.delete('start')
      if (nextFilters.end) next.set('end', nextFilters.end)
      else next.delete('end')
      next.delete('cursors') // 筛选变化回到第一页
      return next
    })
  }

  const goNextPage = (nextAfter: number) => {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      next.set('cursors', [...cursors, nextAfter].join(','))
      return next
    })
  }

  const goPrevPage = () => {
    if (cursors.length <= 1) return
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      const popped = cursors.slice(0, -1)
      if (popped.length <= 1) next.delete('cursors')
      else next.set('cursors', popped.join(','))
      return next
    })
  }

  const resetFilters = () => setFilters(EMPTY_FILTERS)

  return { taskId, filters, cursors, pageNo: cursors.length, setTaskId, setFilters, goNextPage, goPrevPage, resetFilters }
}
