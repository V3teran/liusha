/**
 * 全局漏洞台账域 API 客户端（跨 task/host 全量 + triage 处置）
 *
 * 复用 client.ts 的 fetch 封装（单一 X-API-Key 鉴权口径）。
 */

import { get, patch } from './client'
import type { FindingFilters, FindingListResponse, FindingRow } from './types'

/**
 * 分页拉取全局漏洞台账。可选按 host/severity/status/source 筛选；
 * page/size 缺省时后端落 1 / 50。修复历史缺陷：旧漏洞页走 /sitemap 仅覆盖部分场景，其余漏洞不可见。
 */
export async function listFindings(filters: FindingFilters = {}): Promise<FindingListResponse> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(filters)) {
    if (v) params.set(k, String(v))
  }
  const q = params.toString()
  return get<FindingListResponse>(`/findings${q ? '?' + q : ''}`)
}

/** 台账统计（与列表同筛选口径；status/severity 分布，供头部 chip 全量口径）。 */
export interface FindingStat {
  total: number
  by_status: Record<string, number>
  by_severity: Record<string, number>
}

/** 台账头部统计：与列表同筛选口径的 status/severity 聚合（与翻页无关）。 */
export function getFindingStat(filters: FindingFilters): Promise<FindingStat> {
  const params = new URLSearchParams()
  if (filters.host) params.set('host', filters.host)
  if (filters.severity) params.set('severity', filters.severity)
  if (filters.status) params.set('status', filters.status)
  if (filters.source) params.set('source', filters.source)
  return get(`/findings/stat?${params}`)
}

/**
 * 人工处置一条漏洞（triage）。status 五态之一；severity 传空保留扫描原值、非空覆盖；note 可空。
 * 后端 triaged_at 自动打点并 RETURNING 更新后的行——返回它供前端覆盖乐观值（消除时钟偏差）。
 * 非法 status 返 400、id 不存在返 404。
 */
export async function updateFindingTriage(
  id: string,
  status: string,
  severity = '',
  note = '',
): Promise<FindingRow> {
  const res = await patch<{ finding: FindingRow }>(`/findings/${encodeURIComponent(id)}/status`, {
    status,
    severity,
    note,
  })
  return res.finding
}
