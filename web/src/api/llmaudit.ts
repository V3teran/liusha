/**
 * LLM 调用审计域 API 客户端（全局搜索 / 用量合计 / 单条详情）
 *
 * 复用 client.ts 的 fetch 封装（同源携带会话 Cookie）。
 */

import { get } from './client'
import type {
  LLMInvocationDetail,
  LLMInvocationFilters,
  LLMInvocationSearchStat,
  LLMInvocationsResponse,
} from './types'

function invocationFilterParams(f?: Partial<LLMInvocationFilters>): URLSearchParams {
  const params = new URLSearchParams()
  if (!f) return params
  if (f.task) params.set('task', f.task)
  if (f.conversation) params.set('conversation', f.conversation)
  if (f.role) params.set('role', f.role)
  if (f.model) params.set('model', f.model)
  if (f.onlyErr) params.set('only_err', '1')
  if (f.start) {
    // 前端 datetime-local 格式：2026-01-01T00:00
    // 后端需要 RFC3339：2026-01-01T00:00:00Z
    const dt = new Date(f.start)
    if (!isNaN(dt.getTime())) {
      params.set('start', dt.toISOString())
    }
  }
  if (f.end) {
    const dt = new Date(f.end)
    if (!isNaN(dt.getTime())) {
      params.set('end', dt.toISOString())
    }
  }
  return params
}

/**
 * 按筛选列出调用（全局：不限定任务；可按范围、角色、模型、时间收窄，或按 ID 搜索）。
 */
export async function searchLLMInvocations(
  search: string,
  page = 1,
  size = 0,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationsResponse> {
  const params = invocationFilterParams(filters)
  params.set('q', search)
  if (page > 1) params.set('page', String(page))
  if (size > 0) params.set('size', String(size))
  const q = params.toString()
  return get<LLMInvocationsResponse>(`/llm/invocations/search?${q}`)
}

/**
 * 全局搜索口径的用量合计（整个筛选结果集，与翻页无关）——审计页统计 chip 数据源。
 * 与 searchLLMInvocations 吃同一套筛选参数，统计口径永不与列表打架。
 */
export async function searchLLMInvocationStat(
  search: string,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationSearchStat> {
  const params = invocationFilterParams(filters)
  params.set('q', search)
  return get<LLMInvocationSearchStat>(`/llm/invocations/search/stat?${params.toString()}`)
}

/** 角色 / 模型筛选的选项（来自整个筛选结果集，不是当前页）。 */
export interface LLMInvocationFacets {
  roles: string[]
  models: string[]
}

/**
 * 角色 / 模型筛选的选项：服务端按同一套筛选参数取去重值，每个维度不受自身筛选影响——
 * 选中一个模型后其余模型仍可选（从当前页的行推导时，选中后选项只剩它自己）。
 */
export async function searchLLMInvocationFacets(
  search: string,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationFacets> {
  const params = invocationFilterParams(filters)
  params.set('q', search)
  return get<LLMInvocationFacets>(`/llm/invocations/search/facets?${params.toString()}`)
}

/**
 * 拉取单条 LLM 调用的完整原文（含 messages/result），列表页点击钻取用。
 */
export async function getLLMInvocationDetail(id: number): Promise<LLMInvocationDetail> {
  return get<LLMInvocationDetail>(`/llm/invocations/detail/${id}`)
}
