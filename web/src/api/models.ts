/**
 * 模型模块 API 客户端（provider 部署 / 角色指派 两资源）
 *
 * 复用 client.ts 的 get/post/put fetch 封装（单一 X-API-Key 鉴权口径）。
 * 复用 client.ts 的 get/post/put/del 封装（单一 X-API-Key 鉴权口径；错误响应体含
 * 409 的中文 FK 原因，经 raise 统一读取透出给调用方）。
 *
 * 别名中间层已废弃（0099）：角色一跳直连 provider。
 * 事实源是 DB；写经后端 llmstore 失效广播，runner 进程下次 For(role) 即读到最新路由。
 */

import { get, post, put, del } from './client'
import type {
  ProviderConfig,
  ProviderProbeRequest,
  ProviderTestResult,
  ProviderModelsResult,
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
    code: p.code,
    display_name: p.display_name ?? '',
    type: p.type,
    base_url: p.base_url,
    default_model: p.default_model,
    api_key: p.api_key ?? '',
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
        `/models/providers/${encodeURIComponent(p.code)}`,
        body,
      )
  return res.provider
}

/** 删除 provider；被角色路由 FK 引用时后端 409 → 抛带中文原因的错。 */
export async function deleteProvider(key: string): Promise<void> {
  await del(`/models/providers/${encodeURIComponent(key)}`)
}

// ── 实连探测（测试连接 / 模型列表拉取） ────────────────────────────────────

/**
 * 测试连接：发一条最小 chat completion 验证 base_url+key+model 三者全对。
 * 连接失败不是 HTTP 错——后端统一 200 回 {ok:false, err_msg}，直接返回给调用方原地回显。
 * api_key 留空时后端据 key 用已存密钥（编辑态不必重填）。
 */
export async function testProvider(
  req: ProviderProbeRequest,
): Promise<ProviderTestResult> {
  return post<ProviderTestResult>('/models/providers/test', req)
}

/**
 * 模型探测：拉 provider 可用模型名，供 default_model 下拉。
 * 拉不到（不支持 /models 或临时失败）返回空 models，调用方回退纯手填。
 */
export async function listProviderModels(
  req: ProviderProbeRequest,
): Promise<string[]> {
  return (await post<ProviderModelsResult>('/models/providers/list-models', req))
    .models
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
  const res = await put<{ route: RoleRouteConfig }>(
    `/models/routes/${encodeURIComponent(role)}`,
    { provider_code: providerKey },
  )
  return res.route
}

/** 删除角色路由（删后该档回落标准推理档 medium——隐式默认档；接口幂等）。 */
export async function deleteRoleRoute(role: string): Promise<void> {
  await del(`/models/routes/${encodeURIComponent(role)}`)
}

/**
 * 恢复使用环境变量：清除 UI 设置的加密密钥（密文+尾号），api_key_env 引用保留。
 * 返回更新后的 provider（key_last4 置空、api_key_env 保留）。
 */
export async function restoreProviderEnvKey(code: string): Promise<ProviderConfig> {
  const b = await del<{ provider: ProviderConfig }>(
    `/models/providers/${encodeURIComponent(code)}/key`,
  )
  return b.provider
}

// PUT / DELETE 走 client 统一封装：错误响应体（含 409 的中文 FK 原因）由
// client 的 raise 统一读取抛出，此处不再有专用副本。
