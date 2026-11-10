/**
 * 模型模块 API 客户端（provider 部署 / 角色指派 两资源）
 *
 * 复用 client.ts 的 get/post/put fetch 封装（同源携带会话 Cookie）。
 * 复用 client.ts 的 get/post/put/del 封装（同源携带会话 Cookie；错误响应体含
 * 409 的中文 FK 原因，经 raise 统一读取透出给调用方）。
 *
 * 别名中间层已废弃（0099）：角色一跳直连 provider。
 * 事实源是 DB；写经后端 llmstore 失效广播，runner 进程下次 For(role) 即读到最新路由。
 */

import { get, post, put, del } from './client'
import type {
  Diagnosis,
  ProviderConfig,
  ProviderDiagnoseRequest,
  RoleRouteConfig,
  RoutingResponse,
} from './types'

// ── provider 部署 ───────────────────────────────────────────────────────

/** 拉全量 provider 部署（含 disabled，供路由绑定选择器一次拉全）。 */
export async function listProviders(): Promise<ProviderConfig[]> {
  return (await get<{ providers: ProviderConfig[] }>('/models/providers')).providers
}

/**
 * 保存 provider：新建 POST（标识由后端生成）、编辑 PUT 到既有 code（标识只认路径）。
 * api_key 留空 = 编辑时沿用已存密钥。数字草稿 '' 不会走到这里（providerInvalid 已拦）。
 */
export async function saveProvider(
  p: ProviderConfig,
  isNew: boolean,
): Promise<ProviderConfig> {
  const body = {
    display_name: p.display_name.trim(),
    type: p.type,
    base_url: p.base_url.trim(),
    default_model: p.default_model.trim(),
    api_key: p.api_key ?? '',
    auth_scheme: p.auth_scheme,
    output_limit_param: p.type === 'anthropic' ? 'max_tokens' : p.output_limit_param,
    max_tokens: p.max_tokens === '' ? 0 : p.max_tokens,
    thinking_budget_tokens:
      p.type === 'anthropic' && p.thinking_budget_tokens !== '' ? p.thinking_budget_tokens : 0,
    supports_tools: p.supports_tools,
    supports_vision: p.supports_vision,
    context_window: p.context_window === '' ? 0 : p.context_window,
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

// ── 连接诊断 ─────────────────────────────────────────────────────────────

/**
 * 连接诊断：按表单当前值实测端点、鉴权、模型、工具调用与模型列表，协商出请求画像，
 * 并在「换个写法能通」时给出建议。不落库——结果由调用方回填表单，保存时才生效。
 * 上游的任何失败都在结果的检查项里；只有无法发起（已存 provider 不存在、密钥未配置、
 * 请求不合法）才抛错。
 */
export async function diagnoseProvider(req: ProviderDiagnoseRequest): Promise<Diagnosis> {
  return post<Diagnosis>('/models/providers/diagnose', req)
}

// ── 角色指派（角色 → provider 直连） ──────────────────────────────────────

/** 拉路由全景：角色 → provider 路由表（无别名层）。 */
export async function getRouting(): Promise<RoutingResponse> {
  return get<RoutingResponse>('/models/routing')
}

/** upsert 档 → provider 映射。档名非三档之一 → 400；provider 不存在 → 409，抛中文原因。 */
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

/** 删除档位路由（删后该档按就近降级由相邻档承接；接口幂等）。 */
export async function deleteRoleRoute(role: string): Promise<void> {
  await del(`/models/routes/${encodeURIComponent(role)}`)
}

/**
 * 恢复使用环境变量：清除 UI 设置的加密密钥（密文+尾号），api_key_env 引用保留。
 * 返回更新后的 provider（key_last4 置空、api_key_env 保留）；无环境变量可回落时后端 409。
 */
export async function restoreProviderEnvKey(code: string): Promise<ProviderConfig> {
  const b = await del<{ provider: ProviderConfig }>(
    `/models/providers/${encodeURIComponent(code)}/key`,
  )
  return b.provider
}

// PUT / DELETE 走 client 统一封装：错误响应体（含 409 的中文 FK 原因）由
// client 的 raise 统一读取抛出，此处不再有专用副本。
