import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getLLMInvocationDetail, getLLMInvocationFacets, getLLMInvocationStat, listLLMInvocations } from '@/api/client'
import type { LLMInvocationFilters } from '@/api/types'

// LLM 审计页数据层：统一走 React Query，取代手写 loading/error/竞态处理。
// query key 含 owner + filters + after：任一变化即视为新查询，过期请求的结果自动被丢弃
// （不会出现「快速切换筛选后，旧请求晚回来覆盖新结果」的竞态）。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留在屏幕上，等新数据到达再替换，
// 不必每次都整表闪成 loading 骨架。

export const LLM_AUDIT_PAGE_SIZE = 100

const llmAuditKeys = {
  facets: (owner: string) => ['llm-audit', 'facets', owner] as const,
  list: (owner: string, filters: LLMInvocationFilters, after: number) =>
    ['llm-audit', 'list', owner, filters, after] as const,
  stat: (owner: string, filters: LLMInvocationFilters) => ['llm-audit', 'stat', owner, filters] as const,
  detail: (owner: string, id: number | null) => ['llm-audit', 'detail', owner, id] as const,
}

/** role/model 候选下拉：per-owner 全集，不受当前筛选/分页影响。 */
export function useLlmAuditFacets(owner: string) {
  return useQuery({
    queryKey: llmAuditKeys.facets(owner),
    queryFn: () => getLLMInvocationFacets(owner),
    enabled: !!owner,
  })
}

/** 明细分页列表：吃 owner + 筛选 + 游标。 */
export function useLlmAuditList(owner: string, filters: LLMInvocationFilters, after: number) {
  return useQuery({
    queryKey: llmAuditKeys.list(owner, filters, after),
    queryFn: () => listLLMInvocations(owner, after, LLM_AUDIT_PAGE_SIZE, filters),
    enabled: !!owner,
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
export function useLlmInvocationDetail(owner: string, id: number | null) {
  return useQuery({
    queryKey: llmAuditKeys.detail(owner, id),
    queryFn: () => getLLMInvocationDetail(owner, id as number),
    enabled: !!owner && id != null,
  })
}
