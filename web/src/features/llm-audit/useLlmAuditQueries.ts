import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getLLMInvocationDetail, searchLLMInvocationStat, searchLLMInvocations } from '@/api/client'
import type { LLMInvocationFilters } from '@/api/types'

// LLM 审计页数据层：统一走 React Query。
// query key 含 search + filters + 分页：任一变化即视为新查询，过期请求的结果自动被丢弃
// （不会出现「快速切换筛选后，旧请求晚回来覆盖新结果」的竞态）。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留在屏幕上，等新数据到达再替换，
// 不必每次都整表闪成 loading 骨架。

const llmAuditKeys = {
  search: (search: string, filters: LLMInvocationFilters, page: number, size: number) =>
    ['llm-audit', 'search', search, filters, page, size] as const,
  stat: (search: string, filters: LLMInvocationFilters) =>
    ['llm-audit', 'stat', search, filters] as const,
}

/** 全局搜索查询（不限定 task_id，支持搜索 conversation_id/assignment_id）。
 * 空搜索时返回所有数据（按时间倒序）。
 */
export function useLlmAuditSearch(params: { search: string; page: number; size: number } & LLMInvocationFilters) {
  const { search, page, size, ...filters } = params
  return useQuery({
    queryKey: llmAuditKeys.search(search, filters, page, size),
    queryFn: () => searchLLMInvocations(search, page, size, filters),
    placeholderData: keepPreviousData,
  })
}

/** 全局筛选口径的用量合计（服务端聚合，与翻页无关）——统计 chip 数据源。
 * key 不含分页：统计是整个筛选结果集的合计，翻页不重新请求。
 */
export function useLlmAuditStat(params: { search: string } & LLMInvocationFilters) {
  const { search, ...filters } = params
  return useQuery({
    queryKey: llmAuditKeys.stat(search, filters),
    queryFn: () => searchLLMInvocationStat(search, filters),
    placeholderData: keepPreviousData,
  })
}

/** 单条调用完整原文（含 messages/result），点击行按需拉。id=null 时不发请求。 */
export function useLlmInvocationDetail(id: number | null) {
  return useQuery({
    queryKey: ['llm-audit', 'detail', id] as const,
    queryFn: () => getLLMInvocationDetail(id as number),
    enabled: id != null,
  })
}
