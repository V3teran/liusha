/**
 * 探索图 API 客户端
 */

import { get } from './client'

export interface GraphNode {
  id: string
  task_id: string
  kind: 'objective' | 'action' | 'observation' | 'result'
  content: unknown
  state?: string
  confidence?: string
  complexity?: string
  priority?: string
  source_type: string
  source_id: string
  created_at: string
  updated_at: string
}

export interface GraphEdge {
  src_id: string
  dst_id: string
  rel: 'GENERATES' | 'CONFIRMS' | 'REFUTES' | 'ENABLES' | 'DEPENDS_ON'
  attrs?: Record<string, unknown>
  created_at: string
}

export interface TaskGraph {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

export interface TaskStats {
  objectives: number
  actions: number
  observations: number
  results: number
}

/**
 * 获取任务的完整探索图（节点 + 边）
 */
export async function getTaskGraph(taskId: string): Promise<TaskGraph> {
  return get<TaskGraph>(`/api/v1/tasks/${taskId}/graph`)
}

/**
 * 获取任务的节点统计
 */
export async function getTaskStats(taskId: string): Promise<TaskStats> {
  return get<TaskStats>(`/api/v1/tasks/${taskId}/stats`)
}

/**
 * 获取任务的节点列表（可按类型筛选）
 */
export async function getTaskNodes(taskId: string, kind?: string): Promise<GraphNode[]> {
  const query = kind ? `?kind=${kind}` : ''
  return (await get<{ nodes: GraphNode[] }>(`/api/v1/tasks/${taskId}/nodes${query}`)).nodes
}
