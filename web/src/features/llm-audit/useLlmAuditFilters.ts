import { useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { LLMInvocationFilters } from '@/api/types'
import { DEFAULT_PAGE_SIZE, PAGE_SIZE_OPTIONS } from '@/lib/pageSize'

// 筛选态迁移进 URL search params（对齐 web/patterns.md「筛选/排序/分页应作为 URL state」）：
// 刷新页面、前进/后退、把链接发给同事复核，筛选条件与翻页位置都能还原，不再只存在组件内存里就丢失。
//
// 参数名：task / role / model / only_err / start / end / page / size。
// 用 task 而非 owner——后端早已把 owner_type/owner_id 多态坍缩为统一 task 表（commit a491e8a5），
// 路由也全是 /llm/invocations/:task_id，前端 URL 层不该再用历史上的 owner 叫法。
// offset 分页（非游标）：对齐流量/漏洞模块的「共 X 条 + N/M 页 + 每页条数」体验。
const PARAM_KEYS = ['role', 'model', 'only_err', 'start', 'end'] as const

export interface LlmAuditUrlState {
  taskId: string
  filters: LLMInvocationFilters
  page: number // 1-based
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一
  setTaskId: (taskId: string) => void
  setFilters: (next: LLMInvocationFilters) => void
  setPage: (page: number) => void
  setSize: (size: number) => void
  resetFilters: () => void
}

const EMPTY_FILTERS: LLMInvocationFilters = { role: '', model: '', onlyErr: false, start: '', end: '' }

function numOr(raw: string | null, fallback: number): number {
  const n = Number(raw)
  return Number.isFinite(n) && n > 0 ? n : fallback
}

export function useLlmAuditFilters(): LlmAuditUrlState {
  const [searchParams, setSearchParams] = useSearchParams()

  const taskId = searchParams.get('task') ?? ''
  const page = useMemo(() => numOr(searchParams.get('page'), 1), [searchParams])
  const size = useMemo(() => {
    const raw = numOr(searchParams.get('size'), DEFAULT_PAGE_SIZE)
    return (PAGE_SIZE_OPTIONS as readonly number[]).includes(raw) ? raw : DEFAULT_PAGE_SIZE
  }, [searchParams])
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
      // 换 task：role/model/时间范围/分页是上一个会话的筛选态，不该带过去。
      PARAM_KEYS.forEach((k) => next.delete(k))
      next.delete('page')
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
      next.delete('page') // 筛选变化回到第一页
      return next
    })
  }

  const setPage = (next: number) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      if (next > 1) p.set('page', String(next))
      else p.delete('page')
      return p
    })
  }

  // 换每页条数：回到第一页（否则「第 3 页 · 100 条/页」可能落在数据末尾之外）。
  const setSize = (next: number) => {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev)
      if (next !== DEFAULT_PAGE_SIZE) p.set('size', String(next))
      else p.delete('size')
      p.delete('page')
      return p
    })
  }

  const resetFilters = () => setFilters(EMPTY_FILTERS)

  return { taskId, filters, page, size, setTaskId, setFilters, setPage, setSize, resetFilters }
}
