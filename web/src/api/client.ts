/**
 * API 客户端内核：fetch 封装 + API key 管理。
 *
 * 与 liusha Go 后端通信的统一底座——各业务域端点按域拆分在同级文件
 * （conversations/findings/traffic/credentials/tasks/logs/llmaudit/config/
 * models/settings/explorationgraph），全部复用本文件的 get/post/put/patch/del
 * （单一 X-API-Key 鉴权与错误解析口径）。
 * API key 存储于 sessionStorage，所有非 SSE 请求都带 X-API-Key header。
 */

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
 * 错误响应的统一出口：后端错误体形如 {"error": "<中文消息>"}（respondErr 单出口），
 * 读取后随异常抛出——调用方 catch 到的 message 直接可展示，不再丢成 "GET /x → 500"。
 * 响应体非 JSON / 读取失败时回退状态码文本。
 */
async function raise(res: Response, method: string): Promise<never> {
  let detail = ''
  try {
    const body = (await res.json()) as { error?: string }
    if (body.error) detail = `: ${body.error}`
  } catch {
    /* 错误体不是 JSON（网关层错误等），回退状态码 */
  }
  throw new Error(`${method} ${res.status}${detail}`)
}

/**
 * 发起 GET 请求，自动添加 X-API-Key header。
 * 导出供 config.ts 等同源模块复用（单一鉴权/错误口径，不各自再拼 header）。
 */
export async function get<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path, {
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) await raise(res, 'GET')
  return res.json()
}

/**
 * 发起 POST 请求（JSON body 可选），自动带 X-API-Key。
 * 注意：/conversations/:id/messages 有 409 busy 特例语义，单独在 conversations.ts 实现。
 */
export async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) await raise(res, 'POST')
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
  if (!res.ok) await raise(res, 'PUT')
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
  if (!res.ok) await raise(res, 'PATCH')
  return res.json()
}

/**
 * 发起 DELETE 请求，自动带 X-API-Key。
 * 注意：DELETE /conversations/:id 有 409 哨兵特例语义，单独在 conversations.ts 实现。
 */
export async function del<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) await raise(res, 'DELETE')
  return res.json()
}
