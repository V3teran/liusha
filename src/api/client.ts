/**
 * API 客户端
 *
 * 与 liusha Go 后端通信的 fetch 封装。
 * API key 存储于 sessionStorage，所有非 SSE 请求都带 X-API-Key header。
 */

import type {
  Conversation,
  Message,
  Role,
  OwnerSummary,
  SitemapView,
  AttackGraph,
  LLMInvocationsResponse,
  AgentRunsResponse,
  Identity,
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
 * 启动引导：本地无 key 时，尝试从后端 dev 端点 /viewer-config.json 自动拉 X-API-Key 存入。
 *
 * 后端 dev（设了 LIUSHA_VIEWER_DEV_KEY）会在该未鉴权端点返回 {api_key}，免去手输登录。
 * 失败静默——后端没开 dev autofill / 不可达时不阻塞应用启动（后续调用可能 401）。
 */
export async function bootstrapApiKey(): Promise<void> {
  if (getApiKey()) return
  try {
    const res = await fetch('/api/viewer-config.json')
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
 * 获取对话列表
 */
export async function listConversations(): Promise<Conversation[]> {
  return (await get<{ conversations: Conversation[] }>('/conversations')).conversations
}

/**
 * 获取对话中的消息
 * @param convID 对话 ID
 * @param afterSeq 仅返回 Seq > afterSeq 的消息（默认 0 = 全部）
 */
export async function listMessages(convID: string, afterSeq = 0): Promise<Message[]> {
  return (await get<{ messages: Message[] }>(`/conversations/${convID}/messages?after_seq=${afterSeq}`))
    .messages
}

/**
 * 发起对话扫描
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
 * 多轮：往已有对话追加动作消息。
 * 扫描进行中（409）时抛带 busy 标记的错，前端提示停止后再发。
 * @param convID 对话 ID
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
 * 停止对话关联的扫描。
 * @param convID 对话 ID
 */
export async function abortScan(convID: string): Promise<void> {
  const res = await fetch(`/api/conversations/${convID}/abort`, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`POST /conversations/${convID}/abort → ${res.status}`)
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
 * @returns owner_id 与 hunter_id（据此查任务进度 / agent_runs / llm 审计）
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

/**
 * 拉取执行图（思维链 + 成果链）。read-model 实时投影。
 * @param conv 可选，对话 id（思维链来源）；缺省只出成果链
 * @param type owner 类型，缺省 active_scan
 */
export async function getAttackGraph(
  ownerID: string,
  conv = '',
  type = 'active_scan'
): Promise<AttackGraph> {
  const params = new URLSearchParams()
  if (conv) params.set('conv', conv)
  if (type) params.set('type', type)
  const q = params.toString()
  return get<AttackGraph>(`/attack_graph/${ownerID}${q ? '?' + q : ''}`)
}

/**
 * 拉取该 owner 下全部 LLM 调用审计（后端按 hunter_id 分组）。
 */
export async function listLLMInvocations(ownerID: string): Promise<LLMInvocationsResponse> {
  return get<LLMInvocationsResponse>(`/llm/invocations/${ownerID}`)
}

/**
 * 拉取该 owner 下全部 agent_run（orchestrator_id='' 为根，用于拼任务树）。
 */
export async function listAgentRuns(ownerID: string): Promise<AgentRunsResponse> {
  return get<AgentRunsResponse>(`/agent_runs/${ownerID}`)
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
