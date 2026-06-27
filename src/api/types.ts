/**
 * 后端镜像类型
 *
 * Go 后端 Message 和 ScanEvent 结构体无 json tag，
 * 序列化为大写首字母键。
 */

export type MessageKind = 'message' | 'event'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'
export type ScanEventKind = 'tool_call' | 'tool_result' | 'reasoning' | 'spawn'

/**
 * SSE 事件的元数据（agent 过程事件）
 * 大写键（Go 端序列化格式）
 */
export interface ScanEvent {
  Kind: ScanEventKind
  ToolName: string
  Args: string
  Result: string
  DurationMs: number
  Err: string
  Text: string // reasoning 的推理文字
  AgentName: string // reasoning：产出该推理的 agent 名（orchestrator/exploitation/reconnaissance）
  InTokens: number // reasoning：本次 LLM 输入 token
  OutTokens: number // reasoning：本次 LLM 输出 token
  LatencyMs: number // reasoning：本次 LLM 耗时(ms)
}

/**
 * SSE 消息帧
 * 大写键（Go 端序列化格式）
 */
export interface Message {
  Seq: number
  ID: string
  ConversationID: string
  Role: MessageRole
  Kind: MessageKind
  Content: string
  Metadata: ScanEvent | null
  CreatedAt: string
}

/**
 * 对话会话
 * 大写键（Go 端序列化格式）
 */
export interface Conversation {
  ID: string
  Title: string
  ScanID: string
  RoleID: string
  Status: string // 僵尸字段（默认 active 从不更新）——勿用于显示运行态
  RunStatus?: string // 派生的真实运行态（active/completed/aborted；纯聊天空）——列表显示用此
  CreatedAt: string
  UpdatedAt: string
}

/**
 * 对话用量合计（GET /conversations/:id/usage）
 * 权威口径：后端 SUM llm_invocation + tool_invocation（覆盖纯 tool_call 调用 + 缓存 token），
 * 非前端按 SSE 事件求和。小写键（Go gin.H DTO）。
 */
export interface ConversationUsage {
  conversation_id: string
  owner_id: string // 纯聊天对话为空
  tokens: { in: number; out: number; cached: number; total: number }
  llm_latency_ms: number // 所有 LLM 调用耗时合计（明细）
  tool_duration_ms: number // 所有工具执行耗时合计（明细）
  duration_ms: number // 墙钟：发起→完成真实流逝（"我等了多久"，前端"耗时"展示用此）
  work_ms: number // Σ(LLM latency + 工具 duration)，因子代理并发累加 > 墙钟，仅明细参考
  llm_calls: number
  tool_calls: number
  running: boolean // 是否仍有运行中的扫描（权威：后端 owner 终态）
  status?: string // 真实三态 active/completed/aborted（顶部状态栏三态显示；区别于二元 running）
}

/**
 * 扫描角色（来自 /roles 端点）
 * 小写键（Go DTO 格式）
 */
export interface Role {
  id: string
  name: string
  description: string
  mode: string
}

/**
 * 判断消息是否为 event 类型
 */
export function isEventMessage(m: Message): boolean {
  return m.Kind === 'event'
}

/**
 * 从消息中解析 ScanEvent。
 * 仅当消息类型为 event 且 Metadata 不为空时返回事件；否则返回 null。
 */
export function parseScanEvent(m: Message): ScanEvent | null {
  if (m.Kind !== 'event' || !m.Metadata) return null
  return m.Metadata
}

/* ============================================================
   owner 摘要：被动会话(/session) + 主动扫描共用（GET /session）
   小写键（Go DTO OwnerSummary）。scope 是 jsonb 原文字符串。
   ============================================================ */
export interface OwnerSummary {
  id: string
  scope: string
  status: string
  mode: string
  created_at: string
  expires_at?: string
  ended_at?: string
  error_message?: string
  // 阶段2：passive 会话绑定的对话流 id；前端据此打开会话流实时观察 + 插话。
  conversation_id?: string
}

/* ============================================================
   攻击面 sitemap（GET /sitemap/:owner_id，仅 active 模式）
   domain → endpoint → findings 内嵌树
   ============================================================ */
export interface FindingSummary {
  id: string
  severity: string
  summary: string
  cwe_id?: string
}
export interface SitemapNode {
  kind: string // 'domain' | 'endpoint'
  name: string
  path?: string
  method?: string
  findings?: FindingSummary[]
  children?: SitemapNode[]
}
export interface SitemapView {
  owner_id: string
  host: string
  generated_at: string
  root: SitemapNode | null
}

/* ============================================================
   执行图（GET /attack_graph/:owner_id）：思维链 + 成果链
   read-model 实时投影，不落表（见后端 docs/attack-graph-design.md）
   ============================================================ */
export interface AttackGraphNode {
  id: string
  kind: string // reasoning(想) | action(做+得) | finding(漏洞) | agent(子代理边界)
  parent_id?: string
  agent?: string // 产出该节点的子代理（orchestrator/exploitation/…）
  target?: string // 所属站
  title: string // 短标签
  ref?: string // 指针：finding id / message id，点开取原文
  severity?: string // 漏洞节点配色
  status?: string // 动作节点：done / error
  on_path?: boolean // 成果路径：通向某 finding 的主干（成果优先视图默认展开）；false/缺省=死路，默认折叠
}

export interface AttackGraphEdge {
  from: string
  to: string
  type: string // flow(思维链骨干) | depends_on(成果链) | evidence
}

export interface AttackGraph {
  owner_id: string
  nodes: AttackGraphNode[]
  edges: AttackGraphEdge[]
}

// 里程碑：按子代理聚合的 LLM 一句话摘要（派生层，按需生成）。
export interface Milestone {
  agent: string
  summary: string
  node_count: number
}

/* ============================================================
   LLM 审计（GET /llm/invocations/:owner_id，按 hunter 分组）
   messages/result 为后端 inline 的 jsonb，结构不定 → unknown
   ============================================================ */
export interface LLMInvocation {
  id: string
  hunter_id: string | null
  owner_type: string
  owner_id: string
  provider: string
  model: string
  in_tokens: number
  out_tokens: number
  cached_tokens: number
  latency_ms: number
  finish_reason: string
  error_message: string
  role: string
  messages: unknown
  result: unknown
  created_at: string
}
export interface LLMInvocationGroup {
  hunter_id: string
  count: number
  invocations: LLMInvocation[]
}
export interface LLMInvocationsResponse {
  owner_id: string
  total: number
  groups: LLMInvocationGroup[]
}

/* ============================================================
   Agent 任务树（GET /agent_runs/:owner_id）
   orchestrator_id='' 为根；input/result 为 inline jsonb
   ============================================================ */
export interface AgentRun {
  id: string
  orchestrator_id: string
  role: string // 'orchestrator' | 'exploitation' | 'traffic-analysis'
  status: string // 'pending' | 'running' | 'done' | 'error' | 'aborted'
  input: unknown
  result: unknown
  created_at: string
  updated_at: string
}
export interface AgentRunsResponse {
  owner_id: string
  total: number
  runs: AgentRun[]
}

/* ============================================================
   凭证库（GET/POST/DELETE /credential）
   host → Identity[]（每个 Identity 含多条 Credential）
   ============================================================ */
export interface Credential {
  type: string
  key: string
  value: string
}
export interface Identity {
  name: string
  role: string
  credentials: Credential[]
}
