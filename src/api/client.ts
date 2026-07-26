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
  Role,
  OwnerSummary,
  SitemapView,
  AttackGraph,
  Milestone,
  LLMInvocationsResponse,
  LLMInvocationDetail,
  LLMInvocationStat,
  LLMInvocationFacets,
  LLMInvocationFilters,
  Identity,
  FindingRow,
  FindingFilters,
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
    const res = await fetch('/api/dev-config.json')
    if (!res.ok) return
    const { api_key } = (await res.json()) as { api_key?: string }
    if (api_key) setApiKey(api_key)
  } catch {
    // 静默：dev autofill 未开或后端不可达
  }
}

/**
 * 发起 GET 请求，自动添加 X-API-Key header
 */
async function get<T>(path: string): Promise<T> {
  const res = await fetch('/api' + path, {
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`GET ${path} → ${res.status}`)
  return res.json()
}

/**
 * 发起 POST 请求（JSON body 可选），自动带 X-API-Key。
 * 注意：/chat 与 /conversations/:id/messages 有特殊语义（cookie / 409），各自单独实现。
 */
async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch('/api' + path, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`POST ${path} → ${res.status}`)
  return res.json()
}

/**
 * 发起 DELETE 请求，自动带 X-API-Key。
 */
async function del<T>(path: string): Promise<T> {
  const res = await fetch('/api' + path, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`DELETE ${path} → ${res.status}`)
  return res.json()
}

/**
 * 获取扫描角色列表
 */
export async function listRoles(): Promise<Role[]> {
  return (await get<{ roles: Role[] }>('/roles')).roles
}

/**
 * 获取会话列表
 */
export async function listConversations(): Promise<Conversation[]> {
  return (await get<{ conversations: Conversation[] }>('/conversations')).conversations
}

/**
 * 获取会话中的消息（自动分页拉全）。
 *
 * 后端单次返回上限 500 条（clampLimit），长会话（active 扫描动辄上千条事件）一次拉不完。
 * 故内部循环按 after_seq 翻页直到拉空——否则打开/刷新长会话只显示前 500 条，
 * 停在中途某条（实测停在 orchestrator 收尾报告之前，用户看不到最终结果）。
 *
 * @param convID 会话 ID
 * @param afterSeq 起始游标，仅返回 Seq > afterSeq 的消息（默认 0 = 从头拉全）
 */
export async function listMessages(convID: string, afterSeq = 0): Promise<Message[]> {
  const PAGE = 500 // 与后端 maxListLimit 对齐：返回 < PAGE 即最后一页
  const all: Message[] = []
  let cursor = afterSeq
  for (;;) {
    const page = (
      await get<{ messages: Message[] }>(`/conversations/${convID}/messages?after_seq=${cursor}`)
    ).messages
    if (page.length === 0) break
    all.push(...page)
    cursor = page[page.length - 1].Seq
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
  const res = await fetch(`/api/conversations/${convID}/stream-auth`, {
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
 * @param roleID 角色 ID
 * @returns conversation_id 和 scan_id
 */
export async function startChat(
  brief: string,
  roleID: string
): Promise<{ conversation_id: string; scan_id: string }> {
  const res = await fetch('/api/chat', {
    method: 'POST',
    headers: {
      'X-API-Key': getApiKey(),
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ brief, role_id: roleID }),
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
  content: string
): Promise<{ intent: string; scan_id?: string }> {
  const res = await fetch(`/api/conversations/${convID}/messages`, {
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
  const res = await fetch(`/api/conversations/${convID}/abort`, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`POST /conversations/${convID}/abort → ${res.status}`)
}

// 删除会话及其消息（后端 message FK CASCADE 连带删；不动关联 scan/finding 成果）。
// 关联扫描仍在跑时后端返回 409 → 抛 'SCAN_ACTIVE' 哨兵，调用方提示「先停后删」。
export async function deleteConversation(convID: string): Promise<void> {
  const res = await fetch(`/api/conversations/${convID}`, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (res.status === 409) throw new Error('SCAN_ACTIVE')
  if (!res.ok) throw new Error(`DELETE /conversations/${convID} → ${res.status}`)
}

// 重命名会话标题（PATCH /conversations/:id）。空 title → 后端存 NULL，展示回落首条消息摘要。
export async function renameConversation(convID: string, title: string): Promise<void> {
  const res = await fetch(`/api/conversations/${convID}`, {
    method: 'PATCH',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ title }),
  })
  if (!res.ok) throw new Error(`PATCH /conversations/${convID} → ${res.status}`)
}

/* ============================================================
   被动会话 / 主动扫描（owner）
   ============================================================ */

/**
 * 开启被动会话（按 host 找/建 passive_session，幂等）。
 * @param host 形如 example.com:8080；空则后端返空 id（代理就绪信号）
 */
export async function startPassiveSession(host = ''): Promise<{ owner_id: string }> {
  return post('/scan/passive', { host })
}

/**
 * 列出最近的 owner 会话（被动 + 主动），按 created_at 倒序。
 * @param limit 0 表示用后端默认条数
 */
export async function listSessions(limit = 0): Promise<OwnerSummary[]> {
  const q = limit > 0 ? `?limit=${limit}` : ''
  return (await get<{ sessions: OwnerSummary[] }>(`/session${q}`)).sessions
}

/**
 * 停止（置 aborted）指定 owner 会话。
 */
export async function abortSession(id: string): Promise<void> {
  await post(`/session/${id}/abort`)
}

/**
 * 发起主动扫描。brief 为一句话自然语言任务简报，后端整段透传给 hunter LLM。
 * @returns owner_id 与 hunter_id（据此查任务进度 / llm 审计）
 */
export async function startActiveScan(
  brief: string
): Promise<{ owner_id: string; hunter_id: string }> {
  return post('/scan/active', { brief })
}

/* ============================================================
   攻击面 / LLM 审计 / Agent 任务树（按 owner 只读）
   ============================================================ */

/**
 * 拉取攻击面树（仅 active 模式 owner；passive 会 404）。
 * @param host 可选，按 host 过滤；缺省合并该 owner 全部 host
 */
export async function getSitemap(ownerID: string, host = ''): Promise<SitemapView> {
  const q = host ? `?host=${encodeURIComponent(host)}` : ''
  return get<SitemapView>(`/sitemap/${ownerID}${q}`)
}

/* ============================================================
   全局漏洞台账（漏洞管理页）：跨 task/host 全量 + triage 处置
   ============================================================ */

/**
 * 拉取全局漏洞台账（active + passive 全量）。可选按 host/severity/status/mode 筛选。
 * 修复历史缺陷：旧漏洞页走 /sitemap 仅 active，passive 漏洞（占多数）不可见。
 */
export async function listFindings(filters: FindingFilters = {}): Promise<FindingRow[]> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(filters)) {
    if (v) params.set(k, v)
  }
  const q = params.toString()
  return (await get<{ findings: FindingRow[] }>(`/findings${q ? '?' + q : ''}`)).findings
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
  const res = await fetch(`/api/findings/${id}/status`, {
    method: 'PATCH',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ status, severity, note }),
  })
  if (!res.ok) throw new Error(`PATCH /findings/${id}/status → ${res.status}`)
  return (await res.json()).finding as FindingRow
}

/**
 * 拉取执行图（思维链 + 成果链）。read-model 实时投影。
 * conv 由后端按 task 自解析并在响应 conversation_id 回传——前端无需自己 join 会话列表。
 * 保留 conv 可选参数仅作显式覆盖（一般不传）。
 */
export async function getAttackGraph(ownerID: string, conv = ''): Promise<AttackGraph> {
  const q = conv ? `?conv=${encodeURIComponent(conv)}` : ''
  return get<AttackGraph>(`/attack_graph/${ownerID}${q}`)
}

/**
 * 拉取执行图里程碑摘要（按子代理聚合，LLM 生成）。按需调用——是 LLM 请求，较慢。
 * conv 由后端按 task 自解析，前端一般不传。
 */
export async function getMilestones(ownerID: string, conv = ''): Promise<Milestone[]> {
  const q = conv ? `?conv=${encodeURIComponent(conv)}` : ''
  return (await get<{ milestones: Milestone[] }>(`/attack_graph/${ownerID}/milestones${q}`)).milestones
}

/**
 * 把筛选态序列化成后端 query 参数（列表与统计共用，保证两者口径一致）。
 */
function invocationFilterParams(f?: Partial<LLMInvocationFilters>): URLSearchParams {
  const params = new URLSearchParams()
  if (!f) return params
  if (f.role) params.set('role', f.role)
  if (f.model) params.set('model', f.model)
  if (f.onlyErr) params.set('only_err', '1')
  if (f.start) params.set('start', f.start)
  if (f.end) params.set('end', f.end)
  return params
}

/**
 * 拉取该 task 下 LLM 调用审计（扁平 items，id 游标分页 + 服务端筛选）。
 * afterID：上一页 next_after；0 表示从头拉。列表不含 messages/result 大字段。
 */
export async function listLLMInvocations(
  taskID: string,
  afterID = 0,
  limit = 0,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationsResponse> {
  const params = invocationFilterParams(filters)
  if (afterID > 0) params.set('after', String(afterID))
  if (limit > 0) params.set('limit', String(limit))
  const q = params.toString()
  return get<LLMInvocationsResponse>(`/llm/invocations/${taskID}${q ? '?' + q : ''}`)
}

/**
 * 拉取单条 LLM 调用的完整原文（含 messages/result），列表页点击钻取用。
 */
export async function getLLMInvocationDetail(taskID: string, id: number): Promise<LLMInvocationDetail> {
  return get<LLMInvocationDetail>(`/llm/invocations/${taskID}/invocation/${id}`)
}

/**
 * 拉取该 task 下 LLM 调用的数据库层聚合统计（吃与列表同一套筛选，筛选后统计跟着变）。
 */
export async function getLLMInvocationStat(
  taskID: string,
  filters?: Partial<LLMInvocationFilters>,
): Promise<LLMInvocationStat> {
  const q = invocationFilterParams(filters).toString()
  return get<LLMInvocationStat>(`/llm/invocations/${taskID}/stat${q ? '?' + q : ''}`)
}

/**
 * 拉取筛选下拉候选（服务端 distinct 的 role/model，恒为该 task 全集）。
 */
export async function getLLMInvocationFacets(taskID: string): Promise<LLMInvocationFacets> {
  return get<LLMInvocationFacets>(`/llm/invocations/${taskID}/facets`)
}

/* ============================================================
   凭证库
   ============================================================ */

/**
 * 列出指定 host 下的全部身份（含凭证）。
 */
export async function listCredentials(host: string): Promise<Identity[]> {
  return (await get<{ identities: Identity[] }>(`/credential?host=${encodeURIComponent(host)}`))
    .identities
}

/**
 * 批量保存 host → 身份映射。
 * @param ttlSeconds 0 表示永不过期
 */
export async function saveCredentialsBatch(
  credentials: Record<string, Identity[]>,
  ttlSeconds = 0
): Promise<void> {
  await post('/credential/batch', { ttl_seconds: ttlSeconds, credentials })
}

/**
 * 删除指定 host 下的全部持久化身份。
 */
export async function deleteCredentials(host: string): Promise<void> {
  await del(`/credential?host=${encodeURIComponent(host)}`)
}
