/**
 * 任务模块域 API 客户端（GET/POST /tasks：列表筛选分页 + 单任务详情 + 批量/定时下发）
 *
 * 复用 client.ts 的 fetch 封装（单一 X-API-Key 鉴权口径）。
 */

import { get, post } from './client'
import type {
  TaskCreateRequest,
  TaskCreateResponse,
  TaskFilters,
  TaskListResponse,
  TaskSummary,
} from './types'

/** 任务列表（筛选 + 分页），对齐 listTraffic 形态。 */
export async function listTasks(
  page = 1,
  size = 0,
  filters?: Partial<TaskFilters>,
): Promise<TaskListResponse> {
  const params = new URLSearchParams()
  if (filters?.source) params.set('source', filters.source)
  if (filters?.status) params.set('status', filters.status)
  if (filters?.search) params.set('search', filters.search)
  if (filters?.since) params.set('since', filters.since)
  if (filters?.until) params.set('until', filters.until)
  if (page > 1) params.set('page', String(page))
  if (size > 0) params.set('size', String(size))
  const q = params.toString()
  return get<TaskListResponse>(`/tasks${q ? '?' + q : ''}`)
}

/** 单任务详情（详情板）。 */
export async function getTaskDetail(id: string): Promise<TaskSummary> {
  return get<TaskSummary>(`/tasks/${encodeURIComponent(id)}`)
}

/** 「新建任务」下发：cron 空 = 立即（1 assignment → N task → N 对话）；非空 = 定时模板。 */
export async function createTasks(req: TaskCreateRequest): Promise<TaskCreateResponse> {
  return post<TaskCreateResponse>('/tasks', req)
}
