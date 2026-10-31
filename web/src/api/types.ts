/**
 * 后端镜像类型
 *
 * Go 后端 Message 和 TimelineEventMeta 结构体无 json tag，
 * 序列化为大写首字母键。
 */

export type MessageKind = 'message' | 'event'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'

/**
 * SSE 事件的元数据（agent 过程事件）
 * 小写 snake_case 键（后端手工 map 序列化，JSON 惯例）
 */
// TimelineEventMeta 是会话时间线事件消息（kind=event）的 metadata 载荷：
// 按 kind 分族消费（reasoning/tool_call/tool_result/task_status/finding），
// 各卡片只读自己族的字段。
export interface TimelineEventMeta {
  kind: 'reasoning' | 'tool_call' | 'tool_result' | 'task_status' | 'finding' | string
  status: string // task_status：任务生命周期状态（dispatched/completed/aborted/failed）
  steps: number // task_status/completed：推理步数（与卡片步号同口径：一次 agent 推理=一步）
  actions: number // task_status/completed：完成的 Action 数（报告数据，时间线不渲染）
  findings: number // task_status/completed：坐实发现数（晋升 result 的主张，中性领域词）
  stop_reason: string // task_status/completed：收敛原因（控制面枚举，报告数据，时间线不渲染）
  reason: string // task_status/aborted|failed：原因
  call_id: string // tool_call/tool_result：同一次工具调用的关联 ID（并发同名工具乱序完成时按它精确配对）
  tool_name: string // tool_call/tool_result：工具名
  args: string // tool_call：入参 JSON 原文
  result: string // tool_result：输出预览（超长截断）
  duration_ms: number // tool_result：工具执行耗时(ms)
  err: string // tool_result：错误信息（空=成功）
  summary: string // finding：坐实发现的标题
  severity: string // finding：分级（critical/high/medium/low/info——info 档为非漏洞信息成果）
  host: string // finding：目标主机
  target: string // finding：目标定位（URL/路径等）
  text: string // reasoning 的推理文字（已知协议在后端解析边界转人读叙述；原文在 LLM 审计页按调用可查）
  thinking: string // 模型扩展思考终稿（metadata.thinking，折叠区展示）
  agent_name: string // 产出该事件的 agent 名（planner/executor/evaluator/monitor；聊天助手为 assistant）
  in_tokens: number // reasoning：本轮 LLM 输入 token 累计
  out_tokens: number // reasoning：本轮 LLM 输出 token 累计
  latency_ms: number // reasoning：本轮墙钟耗时(ms)
}

/**
 * SSE 消息帧
 * 字段名与后端 json tag 一致（snake_case，见 httpapi 各响应构造点）
 */
export interface Message {
  seq: number
  id: string
  conversation_id: string
  role: MessageRole
  kind: MessageKind
  content: string
  metadata: TimelineEventMeta | null
  created_at: string
}

/**
 * 会话
 * 字段名与后端 json tag 一致（snake_case，见 httpapi 各响应构造点）
 */
export interface Conversation {
  id: string
  title: string
  task_id: string // 关联扫描 task.id——执行图/攻击面按此匹配会话拿思维链
  run_status?: string // 派生的真实运行态（active/completed/aborted；纯聊天无此键）——列表显示用此
  finding_count?: number // 本会话关联 task 已挖到的漏洞数——流量分析 feed 卡「host · N findings」摘要
  source?: string // 派生的下发来源（manual 主动下发 / auto 被动代理；纯聊天归 manual）——双 tab 分流
  created_at: string
  updated_at: string
}

/**
 * 会话用量合计（GET /conversations/:id/usage）
 * 权威口径：后端 SUM llm_invocation + tool_invocation（覆盖纯 tool_call 调用 + 缓存 token），
 * 非前端按 SSE 事件求和。小写键（Go gin.H DTO）。
 */
export interface ConversationUsage {
  conversation_id: string
  task_id: string // 关联扫描 task；纯聊天会话为空
  tokens: { in: number; out: number; cached: number; total: number }
  llm_latency_ms: number // 所有 LLM 调用耗时合计（明细）
  tool_duration_ms: number // 所有工具执行耗时合计（明细）
  duration_ms: number // 墙钟：发起→完成真实流逝（"我等了多久"，前端"耗时"展示用此）
  work_ms: number // Σ(LLM latency + 工具 duration)，因子代理并发累加 > 墙钟，仅明细参考
  llm_calls: number
  tool_calls: number
  running: boolean // 是否仍有运行中的扫描（权威：后端 task 终态）
  status?: string // 真实三态 active/completed/aborted（顶部状态栏三态显示；区别于二元 running）
}

/* ============================================================
   配置管理（agent 资源 CRUD）
   小写键（Go gin.H DTO：agentJSON 单点序列化）。
   ============================================================ */

// AgentConfig 是智能体全字段形态。function_tools/cli_tools/skills 后端保证非 nil。
// 角色以 code 寻址（planner/executor/evaluator/monitor 四个固定 code），无独立种类字段。
//   - function_tools：内置函数工具集（进程内原生函数 code 列表）
//   - cli_tools     ：外置 CLI 工具集（tools.yaml 名字，严格白名单），空 = 不装配任何外置工具
//   - skills        ：可访问 skill 裸名白名单（渐进式加载：声明的进 system prompt 技能索引并可 read_skill）
export interface AgentConfig {
  id: string
  code: string
  name: string
  description: string
  system_prompt: string
  function_tools: string[]
  cli_tools: string[]
  skills: string[]
  max_iterations: number
  // complexity 复杂度档：simple | medium | complex。决定该智能体 LLM 路由的第一跳档位。
  complexity: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

// 工具种类：function（进程内函数工具）/ cli（外置 CLI 工具白名单项）。
export type ToolKind = 'function' | 'cli'

// Tool 是工具目录一项（GET /tools 列表、AgentsPage function_tools/cli_tools 多选器候选）。
// 源出代码（函数注册表 + tools.yaml），DB 为启动期同步的只读目录。
// Skill 是知识库 skill 一项（GET /skills 列表，AgentsPage skills 多选器候选）。
// code = 裸名寻址键（= 种子目录名，如 browser-use / dom-xss），agent.skills 声明与 read_skill 都用它。
export interface Skill {
  id: string
  code: string
  category: string
  name: string
  description: string
  body: string
  is_builtin: boolean
  enabled: boolean
  created_at?: string
  updated_at?: string
}

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
   解析链：role → 别名（alias）→ provider 部署 code。
   小写键（Go gin.H DTO：providerJSON/aliasJSON/roleRouteJSON 单点序列化）。
   安全铁律：密钥值前端仅在保存时明文提交一次（走 HTTPS），后端 AES-256-GCM 加密落库，
   任何 GET 响应都不回传密钥值；key_present 仅提示该 provider 是否已有可用密钥来源。
   ============================================================ */

// provider 协议类型：openai_compat（OpenAI 兼容族，deepseek/qwen/…）/ anthropic（Claude 原生）。
export type ProviderType = 'openai_compat' | 'anthropic'

// ProviderConfig 是一个 LLM provider 部署（连接参数 + 能力标志）。
// code 是稳定引用键（角色路由 FK 指向它），创建后不可改；后端 JSON 字段即 code。
export interface ProviderConfig {
  code: string
  type: ProviderType
  base_url: string
  default_model: string
  api_key?: string // 仅保存时携带的明文输入；编辑态留空 = 不修改已存密钥。GET 响应不含此字段。
  display_name?: string // 显示名称（昵称）：仅展示用、可随时改；空回退 code 展示
  key_present: boolean // 该 provider 当前是否已有可用密钥来源（加密落库或遗留 ENV）
  key_last4?: string // 已存密钥末 4 位（脱敏辨识用，非机密）；无已存密钥或来自 ENV 时为空
  api_key_env?: string // 密钥的 env 变量名引用（seed 引入）；非空且无密文时密钥走 env 路径
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

// RoleRouteConfig 是「复杂度档/保留槽 → provider」的一行绑定。
// role 列存复杂度档名（simple/medium/complex）或保留槽 __fallback__；agent → 档 的绑定在
// agent.complexity（前端「智能体」页可改），路由解析链：agent.complexity → 档 → provider。
//   complex 深度推理（规划、裁决等长链路；默认档为 medium，未显式归档的 agent 落 medium）
//   medium  标准推理（漏洞检测、工具调用）——隐式默认档
//   simple  轻量快答（督查 / 压缩）
//   __fallback__ 主 provider 重试耗尽后的备份 provider
export interface RoleRouteConfig {
  role: string
  provider_code: string
  created_at?: string
  updated_at?: string
}

// RoutingResponse 是 GET /models/routing 的信封：能力档→provider 路由表。
export interface RoutingResponse {
  routes: RoleRouteConfig[]
}

// 三个复杂度档的字面常量（与后端 llmconfig.ComplexitySimple/Medium/Complex 对齐）：
// 同时是 llm_role_route.role 的路由键——「能力路由」页按它分组建 provider。
// 无保留 role：各档重试耗尽后由后端回落标准推理档（medium）部署。
export const COMPLEXITY_SIMPLE = 'simple'
export const COMPLEXITY_MEDIUM = 'medium'
export const COMPLEXITY_COMPLEX = 'complex'

/* ============================================================
   provider 实连探测（POST /models/providers/test|list-models）：即时反馈，不落库。
   密钥来源：api_key 明文优先（新建/更换密钥）；留空则据 key 用后端已存密钥。
   ============================================================ */

// ProviderProbeRequest 是测试连接 / 模型探测的公共请求体。
export interface ProviderProbeRequest {
  code?: string // 已存 provider code；api_key 留空时后端据此取已存密钥
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
  return m.kind === 'event'
}

/**
 * 从消息中解析 TimelineEventMeta。
 * 仅当消息类型为 event 且 Metadata 不为空时返回事件；否则返回 null。
 */
export function parseEventMeta(m: Message): TimelineEventMeta | null {
  if (m.kind !== 'event' || !m.metadata) return null
  return m.metadata
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
  // evaluation 是复现门裁决评估 jsonb（verdict/reasoning/replay 日志/assert 明细，
  // 含 verification_id 回指），前端通用 KV 渲染。
  evaluation?: Record<string, unknown>
  source: string // manual 主动下发 / auto 被动代理（JOIN assignment 派生）
  status: string // open/confirmed/fixed/false_positive/accepted
  triage_note?: string
  triaged_at?: string | null // 未处置为 null
  first_seen_at: string // 首次发现时间（dedup Update 不变；追踪漏洞存续时长）
  depends_on: string[] // 依赖的 finding ID 数组（组合漏洞依赖链）
  created_at: string
}
// 台账筛选参数（全为可选，省略=不筛该维度）+ 分页。
export interface FindingFilters {
  host?: string
  severity?: string
  status?: string
  source?: string
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
   LLM 审计（全局搜索 GET /llm/invocations/search + 统计 +stat + 详情 detail/:id）
   列表/详情接口分离：列表不带 messages/result（大字段，未用不传），
   详情走 GET /llm/invocations/detail/:id 按需拉。
   筛选（q/role/model/仅错误/时间范围）由服务端做，统计与列表吃同一套参数。
   ============================================================ */
// LLMInvocationSummary 是列表行——不含 messages/result。
export interface LLMInvocationSummary {
  id: number
  request_id: string // 跨系统关联键（db 侧 gen_random_uuid() 生成）
  agent_run_id: string | null
  task_id: string | null
  conversation_id: string | null // 派生字段（JOIN conversation），对话标识
  assignment_id: string | null // 派生字段（JOIN task），批次标识
  provider: string
  model: string
  in_tokens: number
  out_tokens: number
  cached_tokens: number
  latency_ms: number
  finish_reason: string
  error_message: string
  role: string // 调用者角色：planner/exploitation/traffic-analysis 等
  created_at: string
  tool_names: string[] // 库内从 result.tool_calls 派生：这次调用请求了哪些工具
  text_preview: string // 库内从 result.content 派生的前 200 字预览（不传 result 大字段）
}
// LLMInvocationDetail 是点击钻取的完整行（messages/result 为后端 inline jsonb，结构不定 → unknown）。
// source 是下发来源 manual|auto（详情 JOIN assignment 派生）——对话跳转按钮据此定 tab。
export interface LLMInvocationDetail extends LLMInvocationSummary {
  messages: unknown
  result: unknown
  source?: string
}
export interface LLMInvocationsResponse {
  total: number // 跨页总数（对齐流量/漏洞模块的 offset 分页口径）
  page: number
  size: number
  items: LLMInvocationSummary[]
}
// LLMInvocationSearchStat 是 GET /llm/invocations/search/stat 的全局筛选合计
// （整个筛选结果集，与翻页无关）——审计页统计 chip 数据源。
export interface LLMInvocationSearchStat {
  calls: number
  in_tokens: number
  out_tokens: number
  cached_tokens: number
  latency_ms: number
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
   凭证库（GET/PUT/DELETE /credentials…）
   永久存储、无 TTL；值全量明文（操作台语义）。
   ============================================================ */

/** 单条凭证（注入位置 + 键值）。 */
export interface CredentialEntry {
  type: 'headers' | 'query' | 'body' | string
  key: string
  value: string // 全量明文
}

/** 登录身份（真实登录用户名 + 凭证集）+ 详情视图派生字段（kind/task_id/task_status 由用户名派生）。
 *  主机不落在身份载荷里——展示一律取行上的 host（存储键归一的 host[:port]）。 */
export interface CredentialIdentity {
  username: string
  role: string
  credentials: CredentialEntry[]
  created_at?: string
  updated_at?: string
  kind?: 'session' | 'persistent'
  task_id?: string
  task_status?: string // active/completed/aborted/failed/gone
}

/** host 汇总行（枚举只含计数，不含凭证值）。 */
export interface CredentialHostSummary {
  host: string
  usernames: number
  sessions: number
}

/* ============================================================
   系统配置（GET/PUT /settings/compaction|runtime|proxy-filter）
   三组业务旋钮，各字段与后端 settingstore 的 json tag 一一对应。
   写经后端失效广播：compaction/runtime 令 runner 现读即生效，
   proxy_filter 触发 proxy 进程热换流量过滤链（无需重启）。
   ============================================================ */

// CompactionSettings 会话历史压缩旋钮：ReAct 循环用「滚动窗口」压上下文——
// 每步 Generate 前保留 system + 最近 N 条非系统消息，N 即各角色窗口条数。
export interface CompactionSettings {
  planner_window_messages: number // 规划链路滚动窗口条数，>0
  executor_window_messages: number // 执行链路滚动窗口条数，>0
  evaluator_window_messages: number // 裁决链路滚动窗口条数，>0
  monitor_window_messages: number // 监察链路滚动窗口条数，>0
}

// RuntimeSettings 工具运行时旋钮。
export interface RuntimeSettings {
  step_tool_timeout_seconds: number // run_command 超时钳制上限秒（LLM 自传 timeout_seconds 的上界），>0
  step_llm_timeout_seconds: number // 单步 LLM 看门狗超时秒（审计页超时判定同源），>0
  run_tail_bytes: number // run_command 输出保留尾部字节数，>0
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


/* ============================================================
   任务模块（GET/POST /tasks…）
   ============================================================ */

// TaskSummary 是任务列表/详情行：task 本体 + 派生列。
//   - assignment_index/total：批内序号/总数（1 assignment → N task 的 fan-out 关系）
//   - source：manual 主动 / auto 被动——定时克隆（scheduled=true）按业务口径归主动
//   - scheduled/cron：定时模板触发标记 + 来源模板 cron 表达式
//   - step_count：探索图 action 节点数 = 认知循环已执行步数
//   - conversation_id：1 task : 1 对话 的跳转链接（旧数据可能为空）
export interface TaskSummary {
  id: string
  brief: string
  target_host: string
  status: string // active | completed | aborted
  assignment_id: string
  assignment_index: number
  assignment_total: number
  source: string // manual | auto
  scheduled: boolean
  cron: string
  conversation_id: string
  finding_count: number
  step_count: number
  created_at: string
  ended_at: string | null
}

export interface TaskListResponse {
  items: TaskSummary[]
  total: number
  page: number
  size: number
}

// TaskFilters：source 等值 / status 等值 / search 跨 brief+批次ID 子串 /
// since+until 创建时间范围（RFC3339，datetime-local 本地值转 ISO）。
export interface TaskFilters {
  source: string
  status: string
  search: string
  since: string
  until: string
}

// TaskCreateRequest 是「新建任务」弹层提交体：每行一个任务目标。
export interface TaskCreateRequest {
  title: string // 批次名称
  description: string // 批次描述（可空）
  briefs: string[] // 每行一个任务目标
  cron: string // 空 = 立即下发；非空 = 定时模板（5 段 cron）
}

// TaskCreateResponse 两种形态按 scheduled 分流。
export interface TaskCreateResponse {
  scheduled: boolean
  assignment_id?: string // 立即下发：批次 id
  tasks?: { task_id: string; conversation_id: string }[]
  task_count: number
  schedule_id?: string // 定时：模板 id
  next_run_at?: string // 定时：首次触发时间
}

// ── 日志查看 ─────────────────────────────────────────────────────────────

// LogEntry 是一行进程日志（logx JSON lines 的解析形态）。offset 是行首在文件中
// 的字节偏移，作为增量轮询的续读游标；fields 收业务扩展键（task_id 等）。
export interface LogEntry {
  offset: number
  time: string
  level: string
  service: string
  instance: string
  caller?: string
  message: string
  error?: string
  fields?: Record<string, unknown>
}

// LogProcessInfo 是进程日志文件概况；文件缺失时 exists=false、instances 为空。
export interface LogProcessInfo {
  name: string
  exists: boolean
  size_bytes: number
  mod_time: string
  instances: string[]
}

export interface LogReadResponse {
  entries: LogEntry[] // 跨进程按时间升序
  cursors: Record<string, number> // 每进程续读偏移（下轮 cursor 参数回传）
  reset: string[] // 文件比游标短（轮转/截断）而被整段重取的进程
  truncated: string[] // 只扫到尾窗口/超限丢旧行的进程（「窗口外未加载」提示用）
}

// LogFilters：进程/实例/级别等值 + 跨消息子串；空串 = 不筛。
export interface LogFilters {
  process: string
  instance: string
  level: string
  search: string
}
