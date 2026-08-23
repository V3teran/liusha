import { useCallback, useEffect, useRef, useState } from 'react'
import { DEFAULT_PAGE_SIZE } from '@/lib/pageSize'

// 搜索防抖时延（毫秒）：输入停顿后再打服务端，避免逐字请求。
const SEARCH_DEBOUNCE_MS = 300

// 分页取数器：给定 (page, size, q) 返回一页数据与跨页总数。
export type PagedFetcher<T> = (
  page: number,
  size: number,
  q: string,
) => Promise<{ items: T[]; total: number }>

export interface PagedList<T> {
  rows: T[]
  total: number
  totalPages: number
  page: number // 1-based
  setPage: (p: number) => void
  size: number // 每页条数，PAGE_SIZE_OPTIONS 之一（对齐流量/漏洞模块的选择器）
  setSize: (size: number) => void
  query: string // 输入框即时值（未防抖）
  setQuery: (q: string) => void
  loading: boolean
  error: string
  reload: () => void // 保存/删除后重取当前页
}

export interface PagedListOptions {
  size?: number // 初始每页条数；缺省 DEFAULT_PAGE_SIZE（与流量/漏洞模块同一档位集合）
  // 外部过滤标识（如工具页的 kind 段）。变化时回到第 1 页并重取；
  // 参与取数依赖，故 fetcher 需在闭包里读取对应的外部过滤值。
  resetKey?: string
}

// 服务端分页 + 防抖搜索的通用列表状态。搜索词或 resetKey 变化时回到第 1 页；
// 用递增 seq 丢弃过期响应，避免慢请求覆盖新请求（竞态）。
export function usePagedList<T>(
  fetcher: PagedFetcher<T>,
  options: PagedListOptions = {},
): PagedList<T> {
  const { size: initialSize = DEFAULT_PAGE_SIZE, resetKey = '' } = options
  const [rows, setRows] = useState<T[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSizeRaw] = useState(initialSize)
  const [query, setQueryRaw] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // 换每页条数：回到第一页（否则「第 3 页 · 100 条/页」可能落在数据末尾之外）。
  const setSize = (next: number) => {
    setSizeRaw(next)
    setPage(1)
  }

  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  const seqRef = useRef(0)

  // 输入防抖：停顿后同步到 debouncedQuery，并复位到第 1 页。
  useEffect(() => {
    const t = setTimeout(() => {
      setDebouncedQuery(query)
      setPage(1)
    }, SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [query])

  // 外部过滤（resetKey）变化：立即回到第 1 页。取数由 doFetch 的依赖触发。
  const firstRunRef = useRef(true)
  useEffect(() => {
    if (firstRunRef.current) {
      firstRunRef.current = false
      return
    }
    setPage(1)
  }, [resetKey])

  const doFetch = useCallback(async () => {
    const seq = ++seqRef.current
    setLoading(true)
    setError('')
    try {
      const res = await fetcherRef.current(page, size, debouncedQuery)
      if (seq !== seqRef.current) return // 过期响应，丢弃
      setRows(res.items)
      setTotal(res.total)
    } catch (e) {
      if (seq !== seqRef.current) return
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      if (seq === seqRef.current) setLoading(false)
    }
    // resetKey 参与依赖：外部过滤变化时重取（fetcher 闭包读取当前过滤值）。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size, debouncedQuery, resetKey])

  useEffect(() => {
    void doFetch()
  }, [doFetch])

  const totalPages = Math.max(1, Math.ceil(total / size))

  // 删除末页最后一项后总页数缩水，夹紧页码触发重取。
  useEffect(() => {
    if (page > totalPages) setPage(totalPages)
  }, [page, totalPages])

  return {
    rows,
    total,
    totalPages,
    page,
    setPage,
    size,
    setSize,
    query,
    setQuery: setQueryRaw,
    loading,
    error,
    reload: () => void doFetch(),
  }
}
