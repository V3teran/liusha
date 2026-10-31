/**
 * 探索图 API 客户端（会话维度：经 conversation 关联的 task 解析）
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
  rel: 'generates' | 'depends_on' | 'triggers'
  attrs?: Record<string, unknown>
  created_at: string
}

/**
 * 获取会话的完整探索图（节点 + 边；后端经 conversation.task_id 解析）
 */
export async function getConversationGraph(conversationId: string): Promise<ConversationGraph> {
  return get<ConversationGraph>(`/conversations/${conversationId}/exploration/graph`)
}

export interface ConversationGraph {
  nodes: GraphNode[]
  edges: GraphEdge[]
}
