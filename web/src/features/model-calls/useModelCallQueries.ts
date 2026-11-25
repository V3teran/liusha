import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { getLLMInvocationDetail, getLLMInvocationsFacets, getLLMInvocationsStat, listLLMInvocations } from '@/api/modelCalls'
import { getRuntimeSettings } from '@/api/settings'
import type { LLMInvocationFilters } from '@/api/types'

// 模型调用页数据层：统一走 React Query。
// query key 含 search + filters + 分页：任一变化即视为新查询，过期请求的结果自动被丢弃
// （不会出现「快速切换筛选后，旧请求晚回来覆盖新结果」的竞态）。
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留在屏幕上，等新数据到达再替换，
// 不必每次都整表闪成 loading 骨架。

const modelCallKeys = {
  search: (search: string, filters: LLMInvocationFilters, page: number, size: number) =>
    ['model-calls', 'search', search, filters, page, size] as const,
  stat: (search: string, filters: LLMInvocationFilters) =>
    ['model-calls', 'stat', search, filters] as const,
  facets: (search: string, filters: LLMInvocationFilters) =>
    ['model-calls', 'facets', search, filters] as const,
  runtime: () => ['settings', 'runtime'] as const,
}

/** 单步 LLM 看门狗（runtime.step_llm_timeout_seconds）——审计页超时判定的权威值。
 * 与「系统」页共享同一 query key：那边改完这边失效即重读。
 */
export function useRuntimeSettings() {
  return useQuery({
    queryKey: modelCallKeys.runtime(),
    queryFn: getRuntimeSettings,
    staleTime: 60_000,
  })
}

/** 全局搜索查询（不限定 task_id，支持搜索 conversation_id/assignment_id）。
 * 空搜索时返回所有数据（按时间倒序）。
 */
export function useModelCallSearch(params: { search: string; page: number; size: number } & LLMInvocationFilters) {
  const { search, page, size, ...filters } = params
  return useQuery({
    queryKey: modelCallKeys.search(search, filters, page, size),
    queryFn: () => listLLMInvocations(search, page, size, filters),
    placeholderData: keepPreviousData,
  })
}

/** 全局筛选口径的用量合计（服务端聚合，与翻页无关）——统计 chip 数据源。
 * key 不含分页：统计是整个筛选结果集的合计，翻页不重新请求。
 */
export function useModelCallStat(params: { search: string } & LLMInvocationFilters) {
  const { search, ...filters } = params
  return useQuery({
    queryKey: modelCallKeys.stat(search, filters),
    queryFn: () => getLLMInvocationsStat(search, filters),
    placeholderData: keepPreviousData,
  })
}

/** 角色 / 模型筛选的选项（服务端，整个筛选结果集）。key 不含分页：翻页不重新请求。 */
export function useModelCallFacets(params: { search: string } & LLMInvocationFilters) {
  const { search, ...filters } = params
  return useQuery({
    queryKey: modelCallKeys.facets(search, filters),
    queryFn: () => getLLMInvocationsFacets(search, filters),
    placeholderData: keepPreviousData,
  })
}

/** 单条调用完整原文（含 messages/result），点击行按需拉。id=null 时不发请求。 */
export function useLlmInvocationDetail(id: number | null) {
  return useQuery({
    queryKey: ['model-calls', 'detail', id] as const,
    queryFn: () => getLLMInvocationDetail(id as number),
    enabled: id != null,
  })
}
