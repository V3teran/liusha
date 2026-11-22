/**
 * 审计日志 API 客户端（GET /audit：系统级敏感操作的审计链，只读）
 *
 * 复用 client.ts 的 get 封装（同源携带会话 Cookie；错误响应体经 raise 统一读取）。
 */

import { get } from './client'

export interface AuditEvent {
  id: number
  /** 操作者：user:<账号> / runner / system / anonymous */
  actor: string
  action: string
  target_kind: string
  target_id: string
  metadata: Record<string, unknown> | null
  created_at: string
}

export interface AuditListResponse {
  items: AuditEvent[]
  total: number
  page: number
  size: number
  /** 动作词表全集（筛选下拉数据源，与后端写入方同源） */
  actions: string[]
}

export interface AuditFilters {
  action: string
  start: string // datetime-local 原值，发送前换算 ISO
  end: string
}

export async function listAuditEvents(
  actor: string,
  page: number,
  size: number,
  filters?: Partial<AuditFilters>,
): Promise<AuditListResponse> {
  const params = new URLSearchParams()
  if (actor.trim()) params.set('actor', actor.trim())
  if (filters?.action) params.set('action', filters.action)
  if (filters?.start) {
    const dt = new Date(filters.start)
    if (!isNaN(dt.getTime())) params.set('start', dt.toISOString())
  }
  if (filters?.end) {
    const dt = new Date(filters.end)
    if (!isNaN(dt.getTime())) params.set('end', dt.toISOString())
  }
  if (page > 1) params.set('page', String(page))
  if (size > 0) params.set('size', String(size))
  return get<AuditListResponse>(`/audit?${params.toString()}`)
}
