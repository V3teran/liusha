/**
 * 后端镜像类型
 *
 * Go 后端 Message 和 ScanEvent 结构体无 json tag，
 * 序列化为大写首字母键。
 */

export type MessageKind = 'message' | 'event'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'
export type ScanEventKind = 'tool_call' | 'tool_result' | 'reasoning' | 'spawn' | 'compaction'

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
 * 会话
 * 大写键（Go 端序列化格式）
 */
export interface Conversation {
  ID: string
  Title: string
  ScanID: string
  TaskID: string // 关联扫描 task.id（= owner id）——执行图/攻击面按此匹配会话拿思维链
  RoleID: string
  // Status 已退役删除（后端不再返回）——运行态用 RunStatus。
  RunStatus?: string // 派生的真实运行态（active/completed/aborted；纯聊天空）——列表显示用此
  Mode?: string // 派生的模式（active/passive；纯聊天空）——渗透会话页/流量分析页据此分流列表
  FindingCount?: number // 本会话关联 task 已挖到的漏洞数——流量分析 feed 卡「host · N findings」摘要
  CreatedAt: string
  UpdatedAt: string
}

/**
 * 会话用量合计（GET /conversations/:id/usage）
 * 权威口径：后端 SUM llm_invocation + tool_invocation（覆盖纯 tool_call 调用 + 缓存 token），
 * 非前端按 SSE 事件求和。小写键（Go gin.H DTO）。
 */
export interface ConversationUsage {
  conversation_id: string
  owner_id: string // 纯聊天会话为空
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
  // 阶段2：passive 会话绑定的会话流 id；前端据此打开会话流实时观察 + 插话。
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
   全局漏洞台账（GET /findings，跨 task/host，active+passive 全量 + triage）
   ============================================================ */
// FindingTarget 是 finding.target jsonb 的常见形态（LLM 自决，字段可缺）。
export interface FindingTarget {
  path?: string
  method?: string
}
// FindingRow 是台账一行：漏洞主体 + 派生 mode + triage 处置态 + 聚合计数。
export interface FindingRow {
  id: string
  severity: string
  summary: string
  host: string
  cwe_id?: string
  owasp_category?: string
  remediation?: string
  target?: FindingTarget
  // evidence 是 LLM 自由 jsonb（PoC/复现命令/观察等，41 种 key），前端通用 KV 渲染。
  evidence?: Record<string, unknown>
  mode: string // active / passive（关联 task 派生）
  status: string // open/confirmed/fixed/false_positive/accepted
  triage_note?: string
  triaged_at?: string | null // 未处置为 null
  created_at: string
}
// 台账筛选参数（全为可选，省略=不筛该维度）。
export interface FindingFilters {
  host?: string
  severity?: string
  status?: string
  mode?: string
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
  task_id: string
  conversation_id: string // 后端按 task 自解析的思维链会话 id（钻取原文/里程碑用）；空=无绑定会话
  running: boolean // 该 task 是否仍在扫描中（权威：task 终态）——前端据此决定是否轮询
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
   LLM 审计（GET /llm/invocations/:task_id，扁平 items + id 游标分页）
   列表/详情接口分离：列表不带 messages/result（大字段，未用不传），
   详情走 GET /llm/invocations/:task_id/invocation/:id 按需拉。
   筛选（role/model/仅错误/时间范围）由服务端做，统计与列表吃同一套参数。
   ============================================================ */
// LLMInvocationSummary 是列表行——不含 messages/result。
export interface LLMInvocationSummary {
  id: number
  request_id: string // 跨系统关联键（db 侧 gen_random_uuid() 生成）
  hunter_id: string | null
  task_id: string | null
  provider: string
  model: string
  in_tokens: number
  out_tokens: number
  cached_tokens: number
  latency_ms: number
  ttft_ms: number // 首 token 延迟；0 = 未测得 / 非流式（区分"思考慢"与"输出长"）
  is_stream: boolean // 是否流式；决定 ttft_ms 是否有意义
  finish_reason: string
  error_message: string
  role: string // 调用者角色：orchestrator/exploitation/traffic-analysis 等
  created_at: string
  tool_names: string[] // 库内从 result.tool_calls 派生：这次调用请求了哪些工具
  text_preview: string // 库内从 result.content 派生的前 200 字预览（不传 result 大字段）
}
// LLMInvocationDetail 是点击钻取的完整行（messages/result 为后端 inline jsonb，结构不定 → unknown）。
export interface LLMInvocationDetail extends LLMInvocationSummary {
  messages: unknown
  result: unknown
}
export interface LLMInvocationsResponse {
  task_id: string
  total: number // 本页行数（非全量总数——全量看 stat.calls）
  next_after: number // 本页最后一行 id，翻下一页时作 after 参数
  has_more: boolean
  items: LLMInvocationSummary[]
}
// LLMInvocationStat 是 GET /llm/invocations/:task_id/stat 的数据库层聚合（吃同一套筛选参数）。
export interface LLMInvocationStat {
  task_id: string
  calls: number
  in_tokens: number
  out_tokens: number
  cached_tokens: number
  latency_ms: number
}
// LLMInvocationFacets 是筛选下拉候选（服务端 distinct，恒为该 task 全集）。
export interface LLMInvocationFacets {
  task_id: string
  roles: string[]
  models: string[]
}
// LLMInvocationFilters 是前端持有的筛选态，序列化进 query 后由服务端筛。
export interface LLMInvocationFilters {
  role: string
  model: string
  onlyErr: boolean
  start: string // RFC3339；空 = 不限
  end: string
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
