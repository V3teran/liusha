import { useEffect, useState } from 'react'
import { keepPreviousData, useQuery, useQueryClient } from '@tanstack/react-query'
import { listAgentsPaged, listSkillsPaged } from '@/api/config'
import type { AgentConfig, Skill } from '@/api/types'
import { DEFAULT_PAGE_SIZE } from '@/lib/pageSize'

// 搜索防抖时延（毫秒）：输入停顿后再打服务端，避免逐字请求。
const SEARCH_DEBOUNCE_MS = 300

// useDebounced 返回防抖后的值（输入停顿 delay 后才同步）。
function useDebounced(value: string, delay: number): string {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(t)
  }, [value, delay])
  return debounced
}

// usePagedQueryState 是分页列表的页码/条数/搜索词状态机（Agent/Skill 两页共用）：
//   - 搜索词防抖生效后回到第 1 页（防逐键请求）；
//   - 换每页条数回到第 1 页（否则「第 3 页 · 100 条/页」可能落在数据末尾之外）；
//   - 总页数缩水（删末页最后一项）时夹紧页码。
function usePagedQueryState() {
  const [page, setPageRaw] = useState(1)
  const [size, setSizeRaw] = useState(DEFAULT_PAGE_SIZE)
  const [query, setQuery] = useState('')
  const debouncedQuery = useDebounced(query, SEARCH_DEBOUNCE_MS)

  useEffect(() => {
    setPageRaw(1)
  }, [debouncedQuery])

  const setSize = (next: number) => {
    setSizeRaw(next)
    setPageRaw(1)
  }

  return { page, setPageRaw, size, setSize, query, setQuery, debouncedQuery }
}

export interface PagedListHandle<T> {
  rows: T[]
  total: number
  totalPages: number
  page: number // 1-based
  setPage: (p: number) => void
  size: number
  setSize: (size: number) => void
  query: string
  setQuery: (q: string) => void
  loading: boolean
  error: string
  reload: () => void // 保存/删除后重取当前页（失效广播，命中缓存的页立即回填）
}

// useResourcePagedList 把「分页状态机 + React Query」拼成与原 usePagedList 同形的
// 列表句柄：queryKey 含 (page, size, q)，任一变化即新查询、过期响应由 RQ 丢弃
// （取代手写 seq 竞态丢弃）；keepPreviousData 让筛选/翻页时旧数据先留屏。
// reload 走 invalidateQueries——写路径（保存/删除）后失效该资源全部页缓存。
function useResourcePagedList<T>(
  resource: 'agents' | 'skills',
  fetchPage: (page: number, size: number, q: string) => Promise<{ items: T[]; total: number }>,
): PagedListHandle<T> {
  const qc = useQueryClient()
  const { page, setPageRaw, size, setSize, query, setQuery, debouncedQuery } = usePagedQueryState()

  const q = useQuery({
    queryKey: [resource, 'list', page, size, debouncedQuery],
    queryFn: () => fetchPage(page, size, debouncedQuery),
    placeholderData: keepPreviousData,
  })

  const total = q.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / size))

  // 删除末页最后一项后总页数缩水，夹紧页码触发重取。
  useEffect(() => {
    if (page > totalPages) setPageRaw(totalPages)
  }, [page, totalPages])

  return {
    rows: q.data?.items ?? [],
    total,
    totalPages,
    page,
    setPage: setPageRaw,
    size,
    setSize,
    query,
    setQuery,
    loading: q.isFetching,
    error: q.error ? q.error.message : '',
    reload: () => {
      void qc.invalidateQueries({ queryKey: [resource] })
    },
  }
}

/** 智能体分页列表（配置管理页）：React Query 取数（对齐 traffic/finding 等域的统一模式）。 */
export function useAgentsPagedList(): PagedListHandle<AgentConfig> {
  return useResourcePagedList<AgentConfig>('agents', async (page, size, q) => {
    const res = await listAgentsPaged(page, size, q)
    return { items: res.agents, total: res.total }
  })
}

/** 技能分页列表（知识库管理页）：React Query 取数（对齐 traffic/finding 等域的统一模式）。 */
export function useSkillsPagedList(): PagedListHandle<Skill> {
  return useResourcePagedList<Skill>('skills', async (page, size, q) => {
    const res = await listSkillsPaged(page, size, q)
    return { items: res.skills, total: res.total }
  })
}
