/**
 * API 客户端内核：fetch 封装 + 会话过期全局拦截。
 *
 * 与 liusha Go 后端通信的统一底座——各业务域端点按域拆分在同级文件
 * （conversations/findings/traffic/credentials/tasks/logs/llmaudit/config/
 * models/settings/explorationgraph/auth），全部复用本文件的 get/post/put/patch/del
 * （单一错误解析口径）。
 *
 * 鉴权：登录会话 Cookie（liusha_session，HttpOnly）由浏览器随同源请求自动携带，
 * 前端不持有任何凭证。业务请求收到 401 = 会话失效，统一跳转登录页。
 */

/**
 * API_BASE 是后端前缀的唯一权威定义：所有请求路径一律写相对路径（/findings），
 * 前缀由此常量统一拼接——手写 '/api/...' 会双前缀 404（历史教训，见
 * explorationgraph.ts）。
 */
export const API_BASE = '/api'

/**
 * 登录页路由。client 层 401 拦截跳转用，与 router.tsx 的路由定义保持一致。
 */
const LOGIN_PATH = '/login'

/**
 * 会话失效跳登录页。仅对业务请求触发（/auth/* 自身与登录页内不跳，
 * 否则登录失败会被重定向打断）。
 */
function redirectOnSessionExpired(path: string) {
  if (path.startsWith('/auth/') || window.location.pathname.startsWith('/login')) return
  window.location.assign(LOGIN_PATH)
}

/**
 * API 错误：携带 HTTP 状态码，供调用方区分「资源不存在」(404) 与「服务故障」——
 * 两者的处置不同（前者不该提供重试）。message 形如 `GET 404: 资源不存在`，可直接展示。
 */
export class ApiError extends Error {
  readonly status: number

  constructor(method: string, status: number, detail = '') {
    super(`${method} ${status}${detail ? `: ${detail}` : ''}`)
    this.name = 'ApiError'
    this.status = status
  }
}

/**
 * 错误响应的统一出口：后端错误体形如 {"error": "<中文消息>"}（respondErr 单出口），
 * 读取后随异常抛出——调用方 catch 到的 message 直接可展示，不再丢成 "GET /x → 500"。
 * 响应体非 JSON / 读取失败时回退状态码文本。
 * 401（会话失效）在抛错前先全局跳转登录页。
 */
async function raise(res: Response, method: string, path: string): Promise<never> {
  if (res.status === 401) redirectOnSessionExpired(path)
  let detail = ''
  try {
    const body = (await res.json()) as { error?: string }
    if (body.error) detail = body.error
  } catch {
    /* 错误体不是 JSON（网关层错误等），回退状态码 */
  }
  throw new ApiError(method, res.status, detail)
}

/**
 * 发起 GET 请求。
 * 导出供 config.ts 等同源模块复用（单一错误口径，不各自再拼请求）。
 */
export async function get<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path)
  if (!res.ok) await raise(res, 'GET', path)
  return res.json()
}

/**
 * 发起 POST 请求（JSON body 可选）。
 * 注意：/conversations/:id/messages 有 409 busy 特例语义，单独在 conversations.ts 实现。
 */
export async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'POST',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) await raise(res, 'POST', path)
  return res.json()
}

/**
 * 发起 PUT 请求（JSON body），配置管理页 upsert-by-code 用。
 */
export async function put<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'PUT',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) await raise(res, 'PUT', path)
  return res.json()
}

/**
 * 发起 PATCH 请求（单字段/局部更新，如 agent 改能力档）。
 */
export async function patch<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(API_BASE + path, {
    method: 'PATCH',
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) await raise(res, 'PATCH', path)
  return res.json()
}

/**
 * 发起 DELETE 请求。
 * 注意：DELETE /conversations/:id 有 409 哨兵特例语义，单独在 conversations.ts 实现。
 */
export async function del<T>(path: string): Promise<T> {
  const res = await fetch(API_BASE + path, { method: 'DELETE' })
  if (!res.ok) await raise(res, 'DELETE', path)
  return res.json()
}
