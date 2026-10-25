import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getLLMInvocationDetail, getLLMInvocationFacets, getLLMInvocationStat, listLLMInvocations, searchLLMInvocations } from '@/api/client'
import type { LLMInvocationFilters } from '@/api/types'

// LLM 审计页数据层：统一走 React Query，取代手写 loading/error/竞态处理。
// query key 含 owner + filters + after：任一变化即视为新查询，过期请求的结果自动被丢弃
// （不会出现「快速切换筛选后，旧请求晚回来覆盖新结果」的竞态）。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留在屏幕上，等新数据到达再替换，
// 不必每次都整表闪成 loading 骨架。

const llmAuditKeys = {
  facets: (owner: string) => ['llm-audit', 'facets', owner] as const,
  list: (owner: string, filters: LLMInvocationFilters, page: number, size: number) =>
    ['llm-audit', 'list', owner, filters, page, size] as const,
  stat: (owner: string, filters: LLMInvocationFilters) => ['llm-audit', 'stat', owner, filters] as const,
  detail: (owner: string, id: number | null) => ['llm-audit', 'detail', owner, id] as const,
  search: (search: string, filters: LLMInvocationFilters, page: number, size: number) =>
    ['llm-audit', 'search', search, filters, page, size] as const,
}

/** 筛选下拉候选（该 task 下所有 role / model distinct，不受筛选参数影响）。 */
export function useLlmAuditFacets(owner: string) {
  return useQuery({
    queryKey: llmAuditKeys.facets(owner),
    queryFn: () => getLLMInvocationFacets(owner),
    enabled: !!owner,
  })
}

/** 列表查询（offset 分页，对齐流量/漏洞模块）。 */
export function useLlmAuditList(owner: string, filters: LLMInvocationFilters, page: number, size: number) {
  return useQuery({
    queryKey: llmAuditKeys.list(owner, filters, page, size),
    queryFn: () => listLLMInvocations(owner, page, size, filters),
    enabled: !!owner,
    placeholderData: keepPreviousData,
  })
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

/** 数据库层聚合统计：与列表吃同一套筛选（不含游标），筛选后统计跟着变。 */
export function useLlmAuditStat(owner: string, filters: LLMInvocationFilters) {
  return useQuery({
    queryKey: llmAuditKeys.stat(owner, filters),
    queryFn: () => getLLMInvocationStat(owner, filters),
    enabled: !!owner,
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
