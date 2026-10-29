/**
 * API 客户端
 *
 * 与 liusha Go 后端通信的 fetch 封装。
 * API key 存储于 sessionStorage，所有非 SSE 请求都带 X-API-Key header。
 */

import type {
  Conversation,
  ConversationUsage,
  Message,
  AgentConfig,
  LLMInvocationsResponse,
  LLMInvocationDetail,
  LLMInvocationSearchStat,
  LLMInvocationFilters,
  TrafficListResponse,
  TrafficDetail,
  TrafficFilters,
  TaskListResponse,
  TaskSummary,
  TaskFilters,
  TaskCreateRequest,
  TaskCreateResponse,
  CredentialHostSummary,
  CredentialIdentity,
  CredentialEntry,
  FindingRow,
  FindingFilters,
  FindingListResponse,
  Skill,
} from './types'

const KEY_STORAGE = 'liusha_api_key'

/**
 * 设置 API key（存于 sessionStorage）
 */
export function setApiKey(k: string) {
  sessionStorage.setItem(KEY_STORAGE, k)
}

/**
 * 获取 API key；未设置时返回空字符串
 */
export function getApiKey(): string {
  return sessionStorage.getItem(KEY_STORAGE) ?? ''
}

/**
 * 启动引导：本地无 key 时，尝试从后端 dev 端点 /dev-config.json 自动拉 X-API-Key 存入。
 *
 * 后端 dev（设了 LIUSHA_DEV_AUTOFILL）会在该未鉴权端点返回 {api_key}，免去手输登录。
 * 失败静默——后端没开 dev autofill / 不可达时不阻塞应用启动（后续调用可能 401）。
 */
export async function bootstrapApiKey(): Promise<void> {
  if (getApiKey()) return
  try {
    // 特例：显式写 /api（不走 API_BASE）——这是鉴权前的启动引导端点，语义独立。
    const res = await fetch('/api/dev-config.json')
    if (!res.ok) return
    const { api_key } = (await res.json()) as { api_key?: string }
    if (api_key) setApiKey(api_key)
  } catch {
    // 静默：dev autofill 未开或后端不可达
  }
}

/**
 * API_BASE 是后端前缀的唯一权威定义：所有请求路径一律写相对路径（/findings），
 * 前缀由此常量统一拼接——手写 '/api/...' 会双前缀 404（历史教训，见
 * explorationgraph.ts）。
 */
export const API_BASE = '/api'

/**
 * 发起 GET 请求，自动添加 X-API-Key header。
 * 导出供 config.ts 等同源模块复用（单一鉴权/错误口径，不各自再拼 header）。
 */
export async function get<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path, {
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`GET ${path} → ${res.status}`)
  return res.json()
}

/**
 * 发起 POST 请求（JSON body 可选），自动带 X-API-Key。
 * 注意：/chat 与 /conversations/:id/messages 有特殊语义（cookie / 409），各自单独实现。
 */
export async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`POST ${path} → ${res.status}`)
  return res.json()
}

/**
 * 发起 PUT 请求（JSON body），自动带 X-API-Key。配置管理页 upsert-by-code 用。
 */
export async function put<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'PUT',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`PUT ${path} → ${res.status}`)
  return res.json()
}

/**
 * 发起 PATCH 请求（单字段/局部更新，如 agent 改能力档），自动带 X-API-Key。
 */
export async function patch<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'PATCH',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`PATCH ${path} → ${res.status}`)
  return res.json()
}

/**
 * 发起 DELETE 请求，自动带 X-API-Key。
 */
export async function del<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`DELETE ${path} → ${res.status}`)
  return res.json()
}

/**
 * 分页 + 搜索获取智能体（配置管理页用）。
 * 不带 page 的 listAgentConfigs 仍是全量。
 */
export async function listAgentsPaged(
  page: number,
  size: number,
  q = '',
): Promise<{ agents: AgentConfig[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (q) params.set('q', q)
  return get<{ agents: AgentConfig[]; total: number }>(`/agents?${params}`)
}

/**
 * 分页 + 搜索获取技能（技能管理页用）。
 */
export async function listSkillsPaged(
  page: number,
  size: number,
  q = '',
): Promise<{ skills: Skill[]; total: number }> {
  const params = new URLSearchParams({ page: String(page), size: String(size) })
  if (q) params.set('q', q)
  const res = await get<{ skills: Skill[]; total: number }>(`/skills?${params}`)
  return res
}

/**
 * 分页获取会话列表（offset 翻页——会话列表按 updated_at 排序，活跃会话会被顶到最前，
 * 没有稳定单调游标可用，故用 offset；这个数据量级下足够，翻页时小幅重排是可接受的权衡）。
 *
 * source 过滤下沉到服务端（非前端在单页结果上再过滤）：分页边界必须建立在已过滤的集合上，
 * 否则「当前 tab 下共 N 个会话」与实际翻得到的条数会对不上。
 *
 * @param limit 每页条数（后端默认 30）
 * @param offset 跳过条数（默认 0 = 首页）
 * @param source 可选，按下发来源过滤（manual 主动下发 / auto 被动代理；纯聊天归 manual）
 */
export async function listConversations(
  limit = 30,
  offset = 0,
  source = '',
): Promise<{ conversations: Conversation[]; hasMore: boolean; total: number }> {
  const q = new URLSearchParams({ limit: String(limit), offset: String(offset) })
  if (source) q.set('source', source)
  const res = await get<{ conversations: Conversation[] | null; has_more: boolean; total: number }>(`/conversations?${q}`)
  // 后端无数据时 conversations 返回 null（Go 的 nil slice 序列化为 null 而非 []）——
  // 归一为空数组，避免下游 items.length 等消费点炸。
  return { conversations: res.conversations ?? [], hasMore: res.has_more, total: res.total ?? 0 }
}

/**
 * 获取会话中的消息（自动分页拉全）。
 *
 * 后端单次返回上限 500 条（clampLimit），长会话（active 扫描动辄上千条事件）一次拉不完。
 * 故内部循环按 after_seq 翻页直到拉空——否则打开/刷新长会话只显示前 500 条，
 * 停在中途某条（实测停在 planner 收尾报告之前，用户看不到最终结果）。
 *
 * @param convID 会话 ID
 * @param afterSeq 起始游标，仅返回 Seq > afterSeq 的消息（默认 0 = 从头拉全）
 */
export async function listMessages(convID: string, afterSeq = 0): Promise<Message[]> {
  const PAGE = 500 // 与后端 maxListLimit 对齐：返回 < PAGE 即最后一页
  const all: Message[] = []
  let cursor = afterSeq
  for (;;) {
    // 后端零消息时返回 null（Go nil slice）——归一为空数组，防下游 for-of 在 null 上炸
    // （与 listConversations 的 null 归一同源防御；后端亦已归一，此处兜底旧响应）。
    const page = (
      await get<{ messages: Message[] | null }>(`/conversations/${convID}/messages?after_seq=${cursor}`)
    ).messages ?? []
    if (page.length === 0) break
    all.push(...page)
    cursor = page[page.length - 1].seq
    if (page.length < PAGE) break
  }
  return all
}

/**
 * 拉取本会话的用量合计（权威：后端 SUM llm_invocation + tool_invocation）。
 * 用于会话头部 token / 耗时 chip，支持轮询实时刷新。
 */
export async function getConversationUsage(convID: string): Promise<ConversationUsage> {
  return get<ConversationUsage>(`/conversations/${convID}/usage`)
}

/**
 * 为某会话签发/刷新 SSE 鉴权 cookie（HttpOnly）。
 *
 * EventSource 不能带 X-API-Key header，只能靠 cookie 鉴权。打开任意会话前、SSE 断线
 * 重连前调本端点（用 X-API-Key 换取 stream cookie），再开 EventSource——根治"打开旧
 * 会话 / 长扫描 / 重连"实时推送失效。credentials:'include' 让浏览器收下 Set-Cookie。
 */
export async function authStream(convID: string): Promise<void> {
  const res = await fetch(`${API_BASE}/conversations/${convID}/stream-auth`, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey() },
    credentials: 'include',
  })
  if (!res.ok) throw new Error(`stream-auth ${convID} → ${res.status}`)
}

/**
 * 发起会话扫描
 * 成功后后端 Set-Cookie liusha_stream（SSE 鉴权用）
 * @param brief 扫描目标描述
 * @returns conversation_id 和 scan_id（闲聊/qa 意图不下发 task 时 scan_id 为空）
 */
export async function startChat(
  brief: string
): Promise<{ conversation_id: string; scan_id: string }> {
  const res = await fetch(`${API_BASE}/chat`, {
    method: 'POST',
    headers: {
      'X-API-Key': getApiKey(),
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ brief }),
  })
  if (!res.ok) throw new Error(`POST /chat → ${res.status}`)
  return res.json()
}

/**
 * 多轮：往已有会话追加动作消息。
 * 扫描进行中（409）时抛带 busy 标记的错，前端提示停止后再发。
 * @param convID 会话 ID
 * @param content 消息内容
 * @returns intent 和可选的 scan_id
 */
export async function followUp(
  convID: string,
  content: string,
): Promise<{ intent: string; scan_id?: string }> {
  const res = await fetch(`${API_BASE}/conversations/${convID}/messages`, {
    method: 'POST',
    headers: {
      'X-API-Key': getApiKey(),
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ content }),
  })
  if (res.status === 409) {
    const err = new Error('扫描进行中') as Error & { busy?: boolean }
    err.busy = true
    throw err
  }
  if (!res.ok) throw new Error(`POST /conversations/${convID}/messages → ${res.status}`)
  return res.json()
}

/**
 * 停止会话关联的扫描。
 * @param convID 会话 ID
 */
export async function abortScan(convID: string): Promise<void> {
  await post(`/conversations/${convID}/abort`)
}

// 删除会话及其消息（后端 message FK CASCADE 连带删；不动关联 scan/finding 成果）。
// 关联扫描仍在跑时后端返回 409 → 抛 'SCAN_ACTIVE' 哨兵，调用方提示「先停后删」。
export async function deleteConversation(convID: string): Promise<void> {
  const res = await fetch(`${API_BASE}/conversations/${convID}`, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (res.status === 409) throw new Error('SCAN_ACTIVE')
  if (!res.ok) throw new Error(`DELETE /conversations/${convID} → ${res.status}`)
}

// 重命名会话标题（PATCH /conversations/:id）。空 title → 后端存 NULL，展示回落首条消息摘要。
export async function renameConversation(convID: string, title: string): Promise<void> {
  await patch(`/conversations/${convID}`, { title })
}


/* ============================================================
   全局漏洞台账（漏洞页）：跨 task/host 全量 + triage 处置
   ============================================================ */

/**
 * 分页拉取全局漏洞台账。可选按 host/severity/status/source 筛选；
 * page/size 缺省时后端落 1 / 50。修复历史缺陷：旧漏洞页走 /sitemap 仅覆盖部分场景，其余漏洞不可见。
 */
export async function listFindings(filters: FindingFilters = {}): Promise<FindingListResponse> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(filters)) {
    if (v) params.set(k, String(v))
  }
  const q = params.toString()
  return get<FindingListResponse>(`/findings${q ? '?' + q : ''}`)
}


/**
 * 人工处置一条漏洞（triage）。status 五态之一；severity 传空保留扫描原值、非空覆盖；note 可空。
 * 后端 triaged_at 自动打点并 RETURNING 更新后的行——返回它供前端覆盖乐观值（消除时钟偏差）。
 * 非法 status 返 400、id 不存在返 404。
 */
export async function updateFindingTriage(
  id: string,
  status: string,
  severity = '',
  note = '',
): Promise<FindingRow> {
  const res = await fetch(`${API_BASE}/findings/${id}/status`, {
    method: 'PATCH',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ status, severity, note }),
  })
  if (!res.ok) throw new Error(`PATCH /findings/${id}/status → ${res.status}`)
  return (await res.json()).finding as FindingRow
}

function invocationFilterParams(f?: Partial<LLMInvocationFilters>): URLSearchParams {
  const params = new URLSearchParams()
  if (!f) return params
  if (f.role) params.set('role', f.role)
  if (f.model) params.set('model', f.model)
  if (f.onlyErr) params.set('only_err', '1')
  if (f.start) {
    // 前端 datetime-local 格式：2026-01-01T00:00
    // 后端需要 RFC3339：2026-01-01T00:00:00Z
    const dt = new Date(f.start)
    if (!isNaN(dt.getTime())) {
      params.set('start', dt.toISOString())
    }
  }
  if (f.end) {
    const dt = new Date(f.end)
    if (!isNaN(dt.getTime())) {
      params.set('end', dt.toISOString())
    }
  }
  return params
}

/**
 * 全局搜索 LLM 调用（不限定 task_id），支持搜索 task_id/conversation_id/assignment_id。
 */
export async function searchLLMInvocations(
  search: string,
  page = 1,
  size = 0,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationsResponse> {
  const params = invocationFilterParams(filters)
  params.set('q', search)
  if (page > 1) params.set('page', String(page))
  if (size > 0) params.set('size', String(size))
  const q = params.toString()
  return get<LLMInvocationsResponse>(`/llm/invocations/search?${q}`)
}

/**
 * 全局搜索口径的用量合计（整个筛选结果集，与翻页无关）——审计页统计 chip 数据源。
 * 与 searchLLMInvocations 吃同一套筛选参数，统计口径永不与列表打架。
 */
export async function searchLLMInvocationStat(
  search: string,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationSearchStat> {
  const params = invocationFilterParams(filters)
  params.set('q', search)
  return get<LLMInvocationSearchStat>(`/llm/invocations/search/stat?${params.toString()}`)
}

/**
 * 拉取单条 LLM 调用的完整原文（含 messages/result），列表页点击钻取用。
 */
export async function getLLMInvocationDetail(id: number): Promise<LLMInvocationDetail> {
  return get<LLMInvocationDetail>(`/llm/invocations/detail/${id}`)
}

/* ============================================================
   流量模块（GET /traffic 系列）：代理捕获流量只读浏览
   ============================================================ */

// statusClass 单字符大类（'2'..'5'）→ 后端 status_min/max 闭区间（2xx=200..299 等）。
// 空或非法值不落参数（不筛状态）。
function applyStatusClass(params: URLSearchParams, statusClass?: string): void {
  const d = Number(statusClass)
  if (!Number.isInteger(d) || d < 1 || d > 9) return
  params.set('status_min', String(d * 100))
  params.set('status_max', String(d * 100 + 99))
}

function trafficFilterParams(f?: Partial<TrafficFilters>): URLSearchParams {
  const params = new URLSearchParams()
  if (!f) return params
  if (f.method) params.set('method', f.method)
  if (f.contentType) params.set('content_type', f.contentType)
  if (f.search) params.set('search', f.search)
  if (f.since) params.set('since', f.since)
  if (f.until) params.set('until', f.until)
  applyStatusClass(params, f.statusClass)
  return params
}

/**
 * 分页浏览代理捕获流量（全局，跨全部 host）。page 1-based；size 默认后端 50、上限 200。
 * 返回瘦摘要（不含 body/headers）+ 同筛选口径的全局 total。
 */
export async function listTraffic(
  page = 1,
  size = 0,
  filters?: Partial<TrafficFilters>,
): Promise<TrafficListResponse> {
  const params = trafficFilterParams(filters)
  if (page > 1) params.set('page', String(page))
  if (size > 0) params.set('size', String(size))
  const q = params.toString()
  return get<TrafficListResponse>(`/traffic${q ? '?' + q : ''}`)
}

/** 拉取单条流量完整原文（含 request/response body + headers），列表点击钻取用。 */
export async function getTrafficDetail(id: number): Promise<TrafficDetail> {
  return get<TrafficDetail>(`/traffic/${id}`)
}

/** 拉取筛选下拉候选（服务端 distinct 的 content_type，恒为全表全集）。 */
export async function listTrafficContentTypes(): Promise<string[]> {
  return (await get<{ content_types: string[] }>('/traffic/content-types')).content_types
}

/* ============================================================
   凭证库
   ============================================================ */

/* ============================================================
   凭证库（GET /credentials、GET/PUT/DELETE /credentials/:host…）
   凭证值全量明文（操作台要看/要复制原文）；无任何 TTL 语义——
   生命周期由任务终态清理/清扫器/人工删除驱动。
   ============================================================ */

/** host 汇总枚举（只含身份计数，不含任何凭证值）。 */
export async function listCredentialHosts(): Promise<CredentialHostSummary[]> {
  return (await get<{ hosts: CredentialHostSummary[] }>('/credentials')).hosts
}

/** 单 host 身份详情（全量明文，含会话任务状态注入）。 */
export async function getCredentialDetail(
  host: string,
): Promise<{ host: string; identities: CredentialIdentity[] }> {
  return get(`/credentials/${encodeURIComponent(host)}`)
}

/** upsert 单个登录身份（host 经服务端 CanonicalHost 归一）。 */
export async function saveCredentialIdentity(
  host: string,
  identity: { username: string; role?: string; credentials: CredentialEntry[] },
): Promise<void> {
  await put(`/credentials/${encodeURIComponent(host)}/usernames`, identity)
}

/** 删除单个登录身份。 */
export async function deleteCredentialIdentity(host: string, username: string): Promise<void> {
  await del(`/credentials/${encodeURIComponent(host)}/usernames/${encodeURIComponent(username)}`)
}

/** 清空该 host 会话身份（持久身份不动），返回删除数。 */
export async function purgeCredentialSessions(host: string): Promise<number> {
  const r = await del<{ deleted: number }>(`/credentials/${encodeURIComponent(host)}/sessions`)
  return r.deleted
}

/** 清空该 host 全部身份。 */
export async function deleteCredentialHost(host: string): Promise<void> {
  await del(`/credentials/${encodeURIComponent(host)}`)
}


/* ============================================================
   任务模块（GET/POST /tasks…）
   列表筛选分页 + 单任务详情 + 「新建任务」批量/定时下发。
   ============================================================ */

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
