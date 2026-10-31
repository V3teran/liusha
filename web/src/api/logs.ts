/**
 * 日志查看域 API 客户端（GET /logs 系列：进程枚举 + 尾读）
 *
 * 复用 client.ts 的 fetch 封装（单一 X-API-Key 鉴权口径）。
 */

import { get } from './client'
import type { LogFilters, LogProcessInfo, LogReadResponse } from './types'

/** 进程日志文件概况（存在性/大小/近期实例），「日志」页筛选下拉用。 */
export function listLogProcesses(): Promise<{ processes: LogProcessInfo[] }> {
  return get('/logs/processes')
}

/**
 * 尾读进程日志（「日志」页）。
 *
 * cursor 传上次响应的 cursors（name:offset 逗号串）即增量轮询：后端只读增量
 * 字节；文件轮转时响应的 reset 列出被整段重取的进程。首次加载 cursor 传 null。
 */
export function readLogs(
  filters: LogFilters,
  cursor: Record<string, number> | null,
  limit: number,
): Promise<LogReadResponse> {
  const params = new URLSearchParams({ limit: String(limit) })
  if (filters.process) params.set('process', filters.process)
  if (filters.instance) params.set('instance', filters.instance)
  if (filters.level) params.set('level', filters.level)
  if (filters.search) params.set('search', filters.search)
  if (cursor) {
    const cur = Object.entries(cursor)
      .map(([name, offset]) => `${name}:${offset}`)
      .join(',')
    if (cur) params.set('cursor', cur)
  }
  return get(`/logs?${params}`)
}
