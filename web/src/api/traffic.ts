/**
 * 流量模块域 API 客户端（GET /traffic 系列：代理捕获流量只读浏览）
 *
 * 复用 client.ts 的 fetch 封装（单一 X-API-Key 鉴权口径）。
 */

import { get } from './client'
import type { TrafficDetail, TrafficFilters, TrafficListResponse } from './types'

// statusClass 单字符大类（'2'..'5'）→ 后端 status_min/max 闭区间（2xx=200..299 等）。
// 空或非法值不落参数（不筛状态）。
function applyStatusClass(params: URLSearchParams, statusClass?: string): void {
  const d = Number(statusClass)
  if (!Number.isInteger(d) || d < 1 || d > 9) return
  params.set('status_min', String(d * 100))
  params.set('status_max', String(d * 100 + 99))
}

function trafficFilterParams(f?: Partial<TrafficFilters>): URLSearchParams {
  const params = new URLSearchParams()
  if (!f) return params
  if (f.method) params.set('method', f.method)
  if (f.contentType) params.set('content_type', f.contentType)
  if (f.search) params.set('search', f.search)
  if (f.since) params.set('since', f.since)
  if (f.until) params.set('until', f.until)
  applyStatusClass(params, f.statusClass)
  return params
}

/**
 * 分页浏览代理捕获流量（全局，跨全部 host）。page 1-based；size 默认后端 50、上限 200。
 * 返回瘦摘要（不含 body/headers）+ 同筛选口径的全局 total。
 */
export async function listTraffic(
  page = 1,
  size = 0,
  filters?: Partial<TrafficFilters>,
): Promise<TrafficListResponse> {
  const params = trafficFilterParams(filters)
  if (page > 1) params.set('page', String(page))
  if (size > 0) params.set('size', String(size))
  const q = params.toString()
  return get<TrafficListResponse>(`/traffic${q ? '?' + q : ''}`)
}

/** 拉取单条流量完整原文（含 request/response body + headers），列表点击钻取用。 */
export async function getTrafficDetail(id: number): Promise<TrafficDetail> {
  return get<TrafficDetail>(`/traffic/${id}`)
}

/** 拉取筛选下拉候选（服务端 distinct 的 content_type，恒为全表全集）。 */
export async function listTrafficContentTypes(): Promise<string[]> {
  return (await get<{ content_types: string[] }>('/traffic/content-types')).content_types
}
