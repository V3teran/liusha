/**
 * 模型配置 API 客户端（provider / 档位路由 两资源）
 *
 * 复用 client.ts 的 get/post/put/del 封装（同源携带会话 Cookie；错误响应体含
 * 409 的中文 FK 原因，经 raise 统一读取透出给调用方）。
 *
 * 档位（simple/medium/complex）一跳直连 provider。
 * 事实源是 DB；写经后端 llmstore 失效广播，各进程下次按档位解析即读到最新路由。
 */

import { get, post, put, del } from './client'
import type {
  Diagnosis,
  ProviderConfig,
  ProviderDiagnoseRequest,
  ProviderModelList,
  ProviderProbeTarget,
  RetrievalDiagnosis,
  RetrievalModelConfig,
  RoleRouteConfig,
  RoutingResponse,
} from './types'
import type { RetrievalModelDraft } from './types'

// ── provider ───────────────────────────────────────────────────────

/** 拉全量 provider（含 disabled，供路由绑定选择器一次拉全）。 */
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
export async function deleteProvider(code: string): Promise<void> {
  await del(`/models/providers/${encodeURIComponent(code)}`)
}

// ── provider 实测（不落库）──────────────────────────────────────────────

/**
 * 测试连接：以默认模型发对话请求，实测端点、鉴权、模型、工具调用，协商出请求画像，
 * 并在「换个写法能通」时给出建议。结果由调用方回填表单，保存时才生效。上游的任何失败
 * 都在结果的检查项里；只有无法发起（已存 provider 不存在、密钥未配置、请求不合法）才抛错。
 */
export async function diagnoseProvider(req: ProviderDiagnoseRequest): Promise<Diagnosis> {
  return post<Diagnosis>('/models/providers/diagnose', req)
}

/**
 * 拉取上游的模型列表（供选择默认模型），不改写任何配置。端点不提供或拉取失败体现在
 * 结果的 status / detail 里；只有无法发起才抛错（同测试连接）。
 */
export async function listProviderModels(target: ProviderProbeTarget): Promise<ProviderModelList> {
  return post<ProviderModelList>('/models/providers/models', target)
}

// ── 检索模型配置（单行；向量与重排共用一把密钥）──────────────────────

/** 拉检索模型生效配置：地址与模型名（留空已折算为出厂默认）、密钥状态。 */
export async function getRetrievalModel(): Promise<RetrievalModelConfig> {
  return (await get<{ retrieval: RetrievalModelConfig }>('/models/retrieval')).retrieval
}

/**
 * 保存检索模型配置：地址、两个模型名与密钥一起保存；api_key 留空 = 沿用已存密钥。
 * 明文只在本次请求内存在，后端加密落库。更换向量模型时后端作废全部旧向量并立即补齐
 * （即时生效，不必等周期巡检）。
 */
export async function saveRetrievalModel(p: RetrievalModelDraft): Promise<RetrievalModelConfig> {
  return (await put<{ retrieval: RetrievalModelConfig }>('/models/retrieval', p)).retrieval
}

/** 清除检索模型密钥（幂等）：知识库回到字面检索，地址与模型名的自定义保留。只清密钥这一项。 */
export async function clearRetrievalKey(): Promise<void> {
  await del('/models/retrieval/key')
}

/**
 * 检索模型实测（一次向量 + 一次重排，向量含维度校验）：按表单当前值测，api_key 留空测
 * 已存密钥。上游失败在结果的检查项里；只有无法发起（未配置、请求不合法）才抛错。
 */
export async function diagnoseRetrievalModel(
  p: RetrievalModelDraft & { api_key?: string },
): Promise<RetrievalDiagnosis> {
  return post<RetrievalDiagnosis>('/models/retrieval/diagnose', p)
}

// ── 档位路由（档位 → provider 直连） ──────────────────────────────────────

/** 拉路由全景：档位 → provider 路由表（列表与单行的读写是同一个资源 routes）。 */
export async function listRoutes(): Promise<RoutingResponse> {
  return get<RoutingResponse>('/models/routes')
}

/** upsert 档 → provider 映射。档名非三档之一 → 400；provider 不存在 → 409，抛中文原因。 */
export async function saveRoleRoute(
  role: string,
  providerCode: string,
): Promise<RoleRouteConfig> {
  const res = await put<{ route: RoleRouteConfig }>(
    `/models/routes/${encodeURIComponent(role)}`,
    { provider_code: providerCode },
  )
  return res.route
}

/** 删除档位路由（删后该档按就近降级由相邻档承接；接口幂等）。 */
export async function deleteRoleRoute(role: string): Promise<void> {
  await del(`/models/routes/${encodeURIComponent(role)}`)
}

// PUT / DELETE 走 client 统一封装：错误响应体（含 409 的中文 FK 原因）由
// client 的 raise 统一读取抛出，此处不再有专用副本。
