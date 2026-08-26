/**
 * 后端镜像类型
 *
 * Go 后端 Message 和 ScanEvent 结构体无 json tag，
 * 序列化为大写首字母键。
 */

export type MessageKind = 'message' | 'event'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'
export type ScanEventKind = 'tool_call' | 'tool_result' | 'reasoning' | 'spawn' | 'insight' | 'compaction'

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
  AgentName: string // reasoning：产出该推理的 agent 名（planner/exploitation/reconnaissance）
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
  TaskID: string // 关联扫描 task.id（= owner id）——执行图/攻击面按此匹配会话拿思维链
  // Status 已退役删除（后端不再返回）——运行态用 RunStatus。
  RunStatus?: string // 派生的真实运行态（active/completed/aborted；纯聊天空）——列表显示用此
  ScenarioID?: string // 派生的场景 code（关联 task 的 scenario_id；纯聊天空）——按场景分流列表
  FindingCount?: number // 本会话关联 task 已挖到的漏洞数——流量分析 feed 卡「host · N findings」摘要
  Source?: string // 派生的下发来源（manual 主动下发 / auto 被动代理；纯聊天归 manual）——双 tab 分流
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

/* ============================================================
   配置管理（scenario / agent 两资源 CRUD）
   小写键（Go gin.H DTO：scenarioJSON/agentJSON 单点序列化）。
   ScenarioConfig 是场景的唯一形态：GET /scenarios 单一口径返回全量全字段，
   对话 ScenarioPicker 与配置管理页共用（停用场景由 enabled 区分：picker 置灰、页内可编辑）。
   ============================================================ */

// scenario 引擎：
//   - solo ：单一智能体独立执行，由 solo_agent_id 单点指定
//   - swarm：planner + 全部 enabled executor 池，运行时动态调度（无需枚举）
export type ScenarioEngine = 'solo' | 'swarm'
// 智能体种类：planner（规划型，读图产出 Move）/ executor（执行型，执行 Move 产出 Attempt）
export type AgentKind = 'planner' | 'executor'

// ScenarioConfig 是场景全字段形态（GET /scenarios 列表与 /scenarios/:id 单条）。
// solo_agent_id：solo 引擎唯一执行智能体 id；swarm 场景为空串（后端 null 序列化）。
export interface ScenarioConfig {
  id: string
  code: string
  name: string
  description: string
  instruction: string
  engine: ScenarioEngine
  solo_agent_id: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

// AgentConfig 是智能体全字段形态。function_tools/cli_tools 后端保证非 nil。
//   - function_tools：内置函数工具集（进程内原生函数 code 列表）
//   - cli_tools     ：外置 CLI 工具集（tools.yaml 名字，严格白名单），空 = 不装配任何外置工具
export interface AgentConfig {
  id: string
  code: string
  kind: AgentKind
  name: string
  description: string
  body: string
  function_tools: string[]
  cli_tools: string[]
  max_iterations: number
  // tier 能力档：heavy(重推理) | vision(多模态) | light(轻任务)。决定该智能体 LLM 路由的第一跳档位。
  tier: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

// 工具种类：function（进程内函数工具）/ cli（外置 CLI 工具白名单项）。
export type ToolKind = 'function' | 'cli'

// Tool 是工具目录一项（GET /tools 列表、AgentAdmin function_tools/cli_tools 多选器候选）。
// 源出代码（函数注册表 + tools.yaml），DB 为启动期同步的只读目录。
export interface Tool {
  name: string
  kind: ToolKind
  category: string
  description: string
  sort_order: number
  synced_at?: string
}

// ToolListResponse 是 GET /tools 的响应信封（分页时带 total）。
export interface ToolListResponse {
  tools: Tool[]
  total: number
}

// ToolAgent 是工具详情里的一个智能体条目：轻量标识 + 是否已装配该工具（involved）。
// involved 由后端权威计算（按工具 kind 判 function_tools/cli_tools 是否含该工具名）。
export interface ToolAgent {
  code: string
  name: string
  involved: boolean
}

// ToolDetail 是 GET /tools/:name 的响应：工具全字段 + 全量智能体及各自装配态。
export interface ToolDetail {
  tool: Tool
  agents: ToolAgent[]
}

/* ============================================================
   模型模块（GET/POST/PUT/DELETE /models）
   三资源对应后端 llm_provider / llm_alias / llm_role_route（事实源在 DB）。
   解析链：role → 别名（alias）→ provider 部署 key。
   小写键（Go gin.H DTO：providerJSON/aliasJSON/roleRouteJSON 单点序列化）。
   安全铁律：密钥值前端仅在保存时明文提交一次（走 HTTPS），后端 AES-256-GCM 加密落库，
   任何 GET 响应都不回传密钥值；key_present 仅提示该 provider 是否已有可用密钥来源。
   ============================================================ */

// provider 协议类型：openai_compat（OpenAI 兼容族，deepseek/qwen/…）/ anthropic（Claude 原生）。
export type ProviderType = 'openai_compat' | 'anthropic'

// ProviderConfig 是一个 LLM provider 部署（连接参数 + 能力标志）。
// key 是稳定引用键（角色路由 FK 指向它），创建后不可改。
export interface ProviderConfig {
  key: string
  type: ProviderType
  base_url: string
  default_model: string
  api_key?: string // 仅保存时携带的明文输入；编辑态留空 = 不修改已存密钥。GET 响应不含此字段。
  key_present: boolean // 该 provider 当前是否已有可用密钥来源（加密落库或遗留 ENV）
  key_last4?: string // 已存密钥末 4 位（脱敏辨识用，非机密）；无已存密钥或来自 ENV 时为空
  max_tokens: number
  supports_tools: boolean
  supports_vision: boolean
  context_window: number // model 总上下文窗口 tokens（react 历史压缩按此算阈值）
  description: string
  sort_order: number
  enabled: boolean
  created_at?: string
  updated_at?: string
}

// RoleRouteConfig 是「能力档/保留槽 → provider」的一行绑定（0105 能力分档）。
// role 列存能力档名（heavy/vision/light）或保留槽 __fallback__；agent → 档 的绑定固定在后端代码
// （llmcfg.AgentTier），前端只配 档 → provider。解析链：agent → tier → provider（两跳）。
//   heavy   重推理纯文本（隐式默认档，未显式归档的 agent 落此）
//   vision  多模态（browser-use 截图链路）
//   light   轻任务省钱（督查 / 压缩）
//   __fallback__ 主 provider 重试耗尽后的备份 provider
export interface RoleRouteConfig {
  role: string
  provider_key: string
  created_at?: string
  updated_at?: string
}

// RoutingResponse 是 GET /models/routing 的信封：能力档→provider 路由表。
export interface RoutingResponse {
  routes: RoleRouteConfig[]
}

// 三个能力档 + 唯一保留槽的字面常量（与后端 llmcfg.TierHeavy/TierVision/TierLight/RoleFallback 对齐）。
export const TIER_HEAVY = 'heavy'
export const TIER_VISION = 'vision'
export const TIER_LIGHT = 'light'
export const ROLE_FALLBACK = '__fallback__'

/* ============================================================
   provider 实连探测（POST /models/providers/test|list-models）：即时反馈，不落库。
   密钥来源：api_key 明文优先（新建/更换密钥）；留空则据 key 用后端已存密钥。
   ============================================================ */

// ProviderProbeRequest 是测试连接 / 模型探测的公共请求体。
export interface ProviderProbeRequest {
  key?: string // 已存 provider key；api_key 留空时后端据此取已存密钥
  type?: ProviderType
  base_url: string
  default_model?: string // 测试连接必填（指定用哪个 model 验证）；模型探测可空
  api_key?: string // 前端直填明文（优先于已存密钥）
}

// ProviderTestResult 是 POST /models/providers/test 的响应。
// ok=false 时 err_msg 给中文原因（鉴权/找不到/超时/网络），前端原地回显。
export interface ProviderTestResult {
  ok: boolean
  latency_ms: number
  model: string
  err_msg: string
}

// ProviderModelsResult 是 POST /models/providers/list-models 的响应。
// models 为空表示 provider 不支持 /models 或临时失败，前端回退纯手填。
export interface ProviderModelsResult {
  models: string[]
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
   owner 摘要：所有 task 共用（GET /tasks，各场景混列）
   小写键（Go DTO TaskSummary）。scope 是 jsonb 原文字符串。
   ============================================================ */
export interface OwnerSummary {
  id: string
  scope: string
  status: string
  scenario_id: string // 所属场景 code
  created_at: string
  ended_at?: string
  error_message?: string
}

/* ============================================================
   全局漏洞台账（GET /findings，跨 task/host，active+passive 全量 + triage）
   ============================================================ */
// FindingTarget 是 finding.target jsonb 的常见形态（LLM 自决，字段可缺）。
export interface FindingTarget {
  path?: string
  method?: string
}
// FindingRow 是台账一行：漏洞主体 + 派生 source + triage 处置态 + 聚合计数。
export interface FindingRow {
  id: string // uuid 内部主键（drawer/URL 用，不在列表直显）
  seq: number // 对外顺序号（bigserial，列表 # 列直显，报告/会话可引用）
  severity: string
  summary: string
  host: string
  cwe_id?: string
  owasp_category?: string
  remediation?: string
  target?: FindingTarget
  // evidence 是 LLM 自由 jsonb（PoC/复现命令/观察等，41 种 key），前端通用 KV 渲染。
  evidence?: Record<string, unknown>
  scenario_id?: string // 所属场景 code（JOIN task 派生）
  source: string // manual 主动下发 / auto 被动代理（JOIN assignment 派生）
  status: string // open/confirmed/fixed/false_positive/accepted
  triage_note?: string
  triaged_at?: string | null // 未处置为 null
  created_at: string
}
// 台账筛选参数（全为可选，省略=不筛该维度）+ 分页。
export interface FindingFilters {
  host?: string
  severity?: string
  status?: string
  source?: string
  scenario_id?: string // 按来源场景（对话所属场景 code）分流——与会话列表同一分流维度
  page?: number // 1-based
  size?: number
}
// GET /findings 分页响应：seq desc（最新优先）+ 同筛选口径的全局 total。
export interface FindingListResponse {
  findings: FindingRow[]
  total: number
  page: number
  size: number
}

/* ============================================================
   攻击图（GET /attack_graph/:task_id）：L3 世界模型投影
   Verifier 坐实的世界状态（节点）+ 关系（边）+ 取证链（verifications）。
   按 scan_id=assignment_id 归属（一交战一图，跨多阶段 task）；前端选 task，后端解析。
   ============================================================ */
// 5 类持久节点（见后端 worldmodel/model.go §NodeKind）。
export type AttackGraphNodeKind = 'target' | 'asset' | 'credential' | 'access' | 'finding'
// 3 类关系边（见后端 worldmodel/model.go §EdgeRel）。攻击链 = enables 边的路径。
export type AttackGraphEdgeRel =
  | 'derives' // 认知因果：A 推出 B
  | 'enables' // 能力使能：Credential→Access、Access→Asset（攻击链）
  | 'on' // 归属附着：Finding on Asset、Asset on Target
// 节点确证程度（图里只两态；Verifier 通过才置 confirmed）。
export type AttackGraphConfidence = 'confirmed' | 'assumed'

// 多态目标标识（TargetRef）：domain 由 Profile 定义，locator 语义仅由对应 Profile 解释。
export interface AttackGraphRef {
  domain: string // web|binary|cloud|host
  ref_kind: string // endpoint|file|resource|node|...
  locator: string // 域内寻址
}

export interface AttackGraphNode {
  id: string
  seq: number // 对外稳定短号
  kind: AttackGraphNodeKind
  ref: AttackGraphRef
  attrs: Record<string, unknown> // 载荷形状由 kind 决定（finding: severity/summary/cwe_id/... ）
  confidence: AttackGraphConfidence
  verified_by?: string // 指向 verification.id；assumed 节点为空
}

export interface AttackGraphEdge {
  id: string
  rel: AttackGraphEdgeRel
  source: string // wm_node.id（后端 Src→source 单点转换）
  target: string // wm_node.id
  attrs: Record<string, unknown>
}

// Verification 是 Verifier 晋升门的取证记录（可复现交付 + 合规审计的证据链源）。
export interface AttackGraphVerification {
  id: string
  lead_id: string
  primitives: unknown // 回放了哪些 L1 原语
  outcome: 'confirmed' | 'refuted'
  evidence: Record<string, unknown> // 复现证据
  duration_ms: number
  created_at: string
}

export interface AttackGraph {
  task_id: string
  scan_id: string // =assignment_id，图归属键
  nodes: AttackGraphNode[]
  edges: AttackGraphEdge[]
  verifications: AttackGraphVerification[]
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
  agent_id: string | null
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
  role: string // 调用者角色：planner/exploitation/traffic-analysis 等
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
  total: number // 跨页总数（对齐流量/漏洞模块的 offset 分页口径）
  page: number
  size: number
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
   流量模块（GET /traffic 系列）
   proxy_traffic：代理捕获的真实用户流量，按 host 归属、先于 task。只读浏览。
   列表/详情分离：列表瘦摘要（不含 body/headers），详情按需拉完整原文。
   筛选（host/method/path/status 范围）由服务端做，offset 分页。
   ============================================================ */
// TrafficSummary 是流量列表行——不含 body/headers（大字段列表页不展示）。
// url 为完整请求 URL（列表 url 列直显）；content_type 为响应主类型（列表 content-type 列 + 筛选命中值）。
export interface TrafficSummary {
  id: number // bigserial 抓包序号，列表 # 列直显（类 Burp #，稳定可引用）
  host: string
  method: string
  path: string
  url: string
  content_type: string
  status_code: number
  resp_len: number // 响应体字节数，列表「长度」列（humanBytes 格式化）
  captured_at: string
}
// TrafficConsumer 是消费本条流量的 passive task（M:N），附解析出的会话 id 供 chip 跳转。
export interface TrafficConsumer {
  task_id: string
  scenario_id: string
  host: string
  status: string
  conv_id: string // 绑定会话；空表示暂不可跳
}
// TrafficDetail 是点击钻取的完整行——Burp 式整条 raw 报文文本（请求行/头/体一体）。
// url / content_type 继承自 TrafficSummary（content_type 驱动 Pretty 美化）。
export interface TrafficDetail extends TrafficSummary {
  scheme: string
  http_version: string
  request_raw: string // 整条请求报文原文（唯一存储，无冗余拆分字段）
  response_raw: string // 整条响应报文原文
  consumed_by: TrafficConsumer[] // 消费本条的 passive task；空数组=未消费
}
export interface TrafficListResponse {
  items: TrafficSummary[]
  total: number // 同筛选口径的全局总行数
  page: number
  size: number
}
// TrafficFilters 是前端持有的筛选态，序列化进 query 后由服务端筛。
//   - method     ：HTTP 方法等值
//   - contentType：响应 Content-Type 主类型等值（下拉 facet）
//   - statusClass：状态码大类 '' | '2' | '3' | '4' | '5'（映射 status_min/max 区间）
//   - search     ：一个搜索框跨 host + url 通配（'*'），空=不筛
//   - since/until：captured_at 时间区间（RFC3339；datetime-local → toISOString）
export interface TrafficFilters {
  method: string
  contentType: string
  statusClass: string
  search: string
  since: string
  until: string
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

/* ============================================================
   系统配置（GET/PUT /settings/compaction|runtime|proxy-filter）
   三组业务旋钮，各字段与后端 settingstore 的 json tag 一一对应。
   写经后端失效广播：compaction/runtime 令 runner 现读即生效，
   proxy_filter 触发 proxy 进程热换流量过滤链（无需重启）。
   ============================================================ */

// CompactionSettings 会话历史压缩旋钮。
export interface CompactionSettings {
  trigger_ratio: number // 触发压缩的窗口占比，(0,1]
  trailing_budget_ratio: number // 会话历史占窗口比例，(0,1]
  compactor_timeout_seconds: number // 旧会话蒸馏单次 LLM 超时秒，>0
}

// RuntimeSettings 工具运行时旋钮。
export interface RuntimeSettings {
  step_tool_timeout_seconds: number // 单步工具执行兜底超时秒，>0
  run_tail_bytes: number // stdout/stderr 截尾字节数，>0
  findings_limit_in_prompt: number // prompt 注入 finding 的 DB 读上限，>0
}

// ProxyFilterSettings 代理流量过滤规则（黑白名单 + body 上限）。
export interface ProxyFilterSettings {
  allow_hosts: string[] // 非空则仅放行这些 host（白名单）
  exclude_methods: string[] // 排除的 HTTP 方法
  exclude_hosts: string[] // 排除的 host（黑名单）
  exclude_upgrade_protocols: string[] // 排除的 Upgrade 协议
  exclude_suffixes: string[] // 排除的 URL 后缀
  exclude_content_types: string[] // 排除的 Content-Type
  exclude_status_codes: number[] // 排除的响应状态码
  max_request_body_size: number // 请求体切片上限字节，>0
  max_response_body_size: number // 响应体切片上限字节，>0
}

// ControlCommand 控制平面命令类型
export type ControlCommand =
  | 'adjust_goal'
  | 'inject_move'
  | 'pause'
  | 'resume'
  | 'terminate'

// AdjustGoalPayload 调整目标命令的 payload
export interface AdjustGoalPayload {
  new_goal: string
}

// InjectMovePayload 注入 Move 命令的 payload
export interface InjectMovePayload {
  kind: string // MoveKind: enumerate/probe/exploit/escalate/persist
  target: Record<string, unknown> // 目标定位符
  reason?: string
  priority?: number
}

// ControlEvent 控制事件记录
export interface ControlEvent {
  id: string
  task_id: string
  command: ControlCommand
  payload?: AdjustGoalPayload | InjectMovePayload | Record<string, unknown>
  created_at: string
  processed_at?: string
}
