/**
 * 探索图 API 客户端（会话维度：经 conversation 关联的 task 解析）
 */

import { get } from './client'

export type NodeKind = 'objective' | 'action' | 'observation' | 'result'

export interface GraphNode {
  id: string
  task_id: string
  kind: NodeKind
  /** 按 kind 多态的业务载荷（后端 json.RawMessage） */
  content: unknown
  /** objective/action 专属：状态机 */
  state?: string
  /** observation/result 专属：置信度（unverified/verified/refuted/factual） */
  confidence?: string
  /** action 专属：复杂度 */
  complexity?: string
  priority?: string
  source_type: string
  source_id: string
  /** action 专属：依赖的其他 action 节点 ID（调度 DAG 不走边） */
  depends_on?: string[]
  /** action 专属：阻塞原因 */
  blocked_reason?: string
  /** 派生字段（verification_outcome/refute_reason 等），展开在详情面板 */
  metadata?: unknown
  created_at: string
  updated_at: string
}

export interface GraphEdge {
  src_id: string
  dst_id: string
  /** generates 推导链 / triggers 发现滋生；后端约束小写，容忍历史大写数据 */
  rel: string
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

export interface Verification {
  id: string
  task_id: string
  node_id: string
  primitives: unknown
  outcome: 'confirmed' | 'refuted'
  evaluation: unknown
  duration_ms: number
  created_at: string
}

/**
 * 获取会话的验证审计链（每次复现裁决的机器证据；node_id 回挂图节点）
 */
export async function getConversationVerifications(conversationId: string): Promise<{ verifications: Verification[] }> {
  return get(`/conversations/${conversationId}/exploration/verifications`)
}
