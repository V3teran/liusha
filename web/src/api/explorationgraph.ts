/**
 * 探索图 API 客户端（会话维度：经 conversation 关联的 task 解析）
 */

import { get } from './client'

export type NodeKind = 'objective' | 'action' | 'observation' | 'result'

/** 假设观察的验证状态：待验证 → 已坐实（晋升结果）/ 已证伪 */
export type VerificationStatus = 'pending' | 'confirmed' | 'refuted'

/** 事实观察带的线索：这条路走得通、还没走到结论。三样缺一不成线索（写入时把关） */
export interface LeadSpec {
  /** 下一步：沿这条事实具体做什么 */
  next: string
  /** 要弄清的结论 */
  question: string
  /** 推进价值：走通了能换来什么 */
  potential: string
}

/** 线索被规划层关闭时记下的类别与理由 */
export interface LeadClosure {
  kind: string
  reason: string
}

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
  /** 事实观察专属：线索（假设、动作、目标、结果没有）。线索成为结构化字段之前落图的没有三样（都是空串） */
  lead?: LeadSpec
  /** 线索被规划层关闭时记下的类别与理由；没有 = 开着，或由跟进的结局推导关闭 */
  lead_closed?: LeadClosure
  /** action 专属：复杂度 */
  complexity?: string
  /** 主张的优先级（假设与结果）：渗透测试里即严重度。动作与目标没有 */
  priority?: string
  /** 规划者给动作标的加急（一轮至多一个）：执行者的空位不够时先派，仅次于用户指派的 */
  urgent?: boolean
  source_type: string
  /** action 专属：阻塞原因 */
  state_reason?: string
  /** 领域扩展字段（refute_reason、rejected_by 等） */
  metadata?: unknown
  created_at: string
  updated_at: string
}

export interface GraphEdge {
  src_id: string
  dst_id: string
  /** generates 推导链（目标 → 动作 → 观察 → 结果）/ triggers 结果催生方向或动作 / pursues 动作跟进线索（动作 → 线索）；后端约束小写 */
  rel: string
  created_at: string
}

/** 任务的完整探索图（节点 + 边）：探索图属于任务。 */
export async function getTaskGraph(taskId: string): Promise<ExplorationGraph> {
  return get<ExplorationGraph>(`/tasks/${encodeURIComponent(taskId)}/exploration/graph`)
}

export interface ExplorationGraph {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

export interface Verification {
  id: string
  task_id: string
  node_id: string
  repro: unknown
  outcome: 'confirmed' | 'refuted'
  evidence: unknown
  duration_ms: number
  created_at: string
}

/** 任务的验证审计链（每次复现判定的机器证据，时间倒序；node_id 回挂图节点）。 */
export async function getTaskVerifications(taskId: string): Promise<{ verifications: Verification[] }> {
  return get(`/tasks/${encodeURIComponent(taskId)}/exploration/verifications`)
}
