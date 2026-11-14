/**
 * 探索图 API 客户端（会话维度：经 conversation 关联的 task 解析）
 */

import { get } from './client'

export type NodeKind = 'objective' | 'action' | 'observation' | 'result'

/** 假设观察的验证状态：待验证 → 已坐实（晋升结果）/ 已证伪 */
export type VerificationStatus = 'pending' | 'confirmed' | 'refuted'

export interface GraphNode {
  id: string
  task_id: string
  kind: NodeKind
  /** 按 kind 多态的业务载荷（后端 json.RawMessage） */
  content: unknown
  /** objective/action 专属：状态机 */
  state?: string
  /** 假设观察专属：验证状态（事实观察与结果没有） */
  verification_status?: VerificationStatus
  /** action 专属：复杂度 */
  complexity?: string
  priority?: string
  source_type: string
  /** action 专属：阻塞原因 */
  blocked_reason?: string
  /** 领域扩展字段（refute_reason 等） */
  metadata?: unknown
  created_at: string
  updated_at: string
}

export interface GraphEdge {
  src_id: string
  dst_id: string
  /** generates 推导链 / triggers 发现滋生 / supersedes 目标吸收（新 → 旧）；后端约束小写，容忍历史大写数据 */
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
 * 获取会话的验证审计链（每次复现判定的机器证据；node_id 回挂图节点）
 */
export async function getConversationVerifications(conversationId: string): Promise<{ verifications: Verification[] }> {
  return get(`/conversations/${conversationId}/exploration/verifications`)
}
