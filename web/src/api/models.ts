/**
 * 模型模块 API 客户端（provider 部署 / 角色指派 两资源）
 *
 * 复用 client.ts 的 get/post/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 单独实现的 putModel/delModel：撞 FK RESTRICT/违反（后端 409 + 中文 error）时需把中文原因
 * 透出给调用方（通用 del 只抛 HTTP 状态码，丢了中文原因）。
 *
 * 别名中间层已废弃（0099）：角色一跳直连 provider。
 * 事实源是 DB；写经后端 llmstore 失效广播，runner 进程下次 For(role) 即读到最新路由。
 */

import { get, post, put, getApiKey } from './client'
import type {
  ProviderConfig,
  RoleRouteConfig,
  RoutingResponse,
} from './types'

// ── provider 部署 ───────────────────────────────────────────────────────

/** 拉全量 provider 部署（含 disabled，供路由绑定选择器一次拉全）。 */
export async function listProviders(): Promise<ProviderConfig[]> {
  return (await get<{ providers: ProviderConfig[] }>('/models/providers')).providers
}

/** 保存 provider：均走 upsert-by-key——新建 POST、已有 key 编辑 PUT。 */
export async function saveProvider(
  p: ProviderConfig,
  isNew: boolean,
): Promise<ProviderConfig> {
  const body = {
    key: p.key,
    type: p.type,
    base_url: p.base_url,
    default_model: p.default_model,
    api_key_env: p.api_key_env,
    max_tokens: p.max_tokens,
    supports_tools: p.supports_tools,
    supports_vision: p.supports_vision,
    context_window: p.context_window,
    description: p.description,
    sort_order: p.sort_order,
    enabled: p.enabled,
  }
  const res = isNew
    ? await post<{ provider: ProviderConfig }>('/models/providers', body)
    : await put<{ provider: ProviderConfig }>(
        `/models/providers/${encodeURIComponent(p.key)}`,
        body,
      )
  return res.provider
}

/** 删除 provider；被角色路由 FK 引用时后端 409 → 抛带中文原因的错。 */
export async function deleteProvider(key: string): Promise<void> {
  await delModel(`/models/providers/${encodeURIComponent(key)}`)
}

// ── 角色指派（角色 → provider 直连） ──────────────────────────────────────

/** 拉路由全景：角色 → provider 路由表（无别名层）。 */
export async function getRouting(): Promise<RoutingResponse> {
  return get<RoutingResponse>('/models/routing')
}

/** upsert 角色 → provider 映射。provider_key 不存在时后端 409 → 抛中文原因。 */
export async function saveRoleRoute(
  role: string,
  providerKey: string,
): Promise<RoleRouteConfig> {
  const res = await putModel<{ route: RoleRouteConfig }>(
    `/models/routes/${encodeURIComponent(role)}`,
    { provider_key: providerKey },
  )
  return res.route
}

/** 删除角色路由（删后该 role 回退 __default__ 角色的 provider）。 */
export async function deleteRoleRoute(role: string): Promise<void> {
  await delModel(`/models/routes/${encodeURIComponent(role)}`)
}

// ── 带中文原因的 PUT / DELETE（409 语义） ─────────────────────────────────

/**
 * 模型模块专用 PUT：409（撞 FK，provider 不存在）时读后端中文 error 抛出，
 * 供调用方 alert 提示「先建对应 provider」。其他非 2xx 抛通用状态码错。
 */
async function putModel<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch('/api' + path, {
    method: 'PUT',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (res.ok) return res.json() as Promise<T>
  if (res.status === 409) {
    const b = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(b.error || '关联对象不存在')
  }
  throw new Error(`PUT ${path} → ${res.status}`)
}

/**
 * 模型模块专用 DELETE：409（被下游 FK RESTRICT 引用）时读后端中文 error 抛出，
 * 供调用方 alert 提示「先解除引用再删」。其他非 2xx 抛通用状态码错。
 */
async function delModel(path: string): Promise<void> {
  const res = await fetch('/api' + path, {
    method: 'DELETE',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (res.ok) return
  if (res.status === 409) {
    const b = (await res.json().catch(() => ({}))) as { error?: string }
    throw new Error(b.error || '该对象仍被引用，无法删除')
  }
  throw new Error(`DELETE ${path} → ${res.status}`)
}
