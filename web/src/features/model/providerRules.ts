import type { ProviderConfig, ProviderType } from '@/api/types'

// provider 配置的纯规则：与后端同名规则一一对应（llmconfig.NormalizeBaseURL /
// llmconfig.ProviderParams.Validate / framework/llm.OperationURL），前端用于实时预览
// 「实际请求」与就地校验。只认协议，不认任何具体服务。

/** 用户常把完整端点粘进 Base URL 时带上的操作路径——由 SDK 自行拼接，不属于 Base URL。 */
const OPERATION_SUFFIXES = ['/chat/completions', '/completions', '/messages', '/models']

/**
 * 按协议规则归一 Base URL：去首尾空白与末尾斜杠、去掉误粘的操作路径；Anthropic 协议
 * 再去掉末尾的 /v1（SDK 自行拼 /v1/messages）。无法解析的地址原样返回。
 */
export function normalizeBaseUrl(protocol: ProviderType | '', raw: string): string {
  const trimmed = raw.trim()
  let u: URL
  try {
    u = new URL(trimmed)
  } catch {
    return trimmed
  }
  if (!u.host) return trimmed
  let path = u.pathname.replace(/\/+$/, '')
  const lower = path.toLowerCase()
  const suffix = OPERATION_SUFFIXES.find((s) => lower.endsWith(s))
  if (suffix) path = path.slice(0, path.length - suffix.length)
  if (protocol === 'anthropic' && path.toLowerCase().endsWith('/v1')) path = path.slice(0, -3)
  path = path.replace(/\/+$/, '')
  return `${u.protocol}//${u.host}${path}${u.search}`
}

/** 协议对话操作的实际请求地址（与 SDK 拼接规则一致）。 */
export function chatRequestUrl(protocol: ProviderType, baseUrl: string): string {
  const base = baseUrl.replace(/\/+$/, '')
  return protocol === 'anthropic' ? `${base}/v1/messages` : `${base}/chat/completions`
}

/** Base URL 必须是带主机名的完整 http(s) 地址；空值不报（必填由 providerInvalid 兜）。 */
export function baseUrlError(raw: string): string | undefined {
  const v = raw.trim()
  if (!v) return undefined
  try {
    const u = new URL(v)
    if ((u.protocol === 'http:' || u.protocol === 'https:') && u.host) return undefined
  } catch {
    // 落到下方统一提示
  }
  return '需填完整地址，以 http:// 或 https:// 开头'
}

// 扩展思考预算的协议约束（与后端同口径）：开启时 ≥ 1024，且小于单次最大输出 tokens
// （max_tokens 为 0/空时运行期按 4096 发送）。
const MIN_THINKING_BUDGET = 1024
const DEFAULT_MAX_TOKENS = 4096

/** 扩展思考预算校验：仅 Anthropic 协议生效；0 = 关闭。 */
export function thinkingBudgetError(d: ProviderConfig): string | undefined {
  if (d.type !== 'anthropic') return undefined
  const budget = d.thinking_budget_tokens
  if (budget === '' || budget === 0) return undefined
  if (budget < 0) return '不能为负'
  if (budget < MIN_THINKING_BUDGET) return `开启时至少 ${MIN_THINKING_BUDGET}`
  const maxTokens = typeof d.max_tokens === 'number' && d.max_tokens > 0 ? d.max_tokens : DEFAULT_MAX_TOKENS
  if (budget >= maxTokens) return `必须小于单次最大输出 tokens（当前 ${maxTokens}）`
  return undefined
}

/**
 * 保存前置：协议已选、base_url/default_model 必填且格式合法、context_window > 0、
 * 思考预算满足协议约束（与后端校验对齐）。新建必须带密钥；编辑留空 = 沿用已存密钥。
 */
export function providerInvalid(d: ProviderConfig | null, isNew: boolean): boolean {
  if (!d) return true
  const cw = d.context_window
  return (
    !d.type ||
    !d.display_name.trim() ||
    !d.base_url.trim() ||
    !!baseUrlError(d.base_url) ||
    !d.default_model.trim() ||
    (isNew && !d.api_key) ||
    // 数字字段编辑期允许空串草稿（NumberField 契约），保存时视为未填（无效）
    !(typeof cw === 'number' && cw > 0) ||
    !!thinkingBudgetError(d)
  )
}

/** 新建 provider 的空白初值：协议不预设（必须由用户选择），能力取保守值。 */
export function blankProvider(): ProviderConfig {
  return {
    code: '',
    display_name: '',
    type: '',
    base_url: '',
    default_model: '',
    api_key: '',
    key_present: false,
    auth_scheme: 'bearer',
    output_limit_param: 'max_tokens',
    max_tokens: 4096,
    thinking_budget_tokens: 0,
    supports_tools: true,
    supports_vision: false,
    context_window: 65536,
    description: '',
    sort_order: 0,
    enabled: true,
  }
}

/** 协议的标准鉴权方式（选协议时作为初值，运行诊断可改写）。 */
export function defaultAuthScheme(protocol: ProviderType): ProviderConfig['auth_scheme'] {
  return protocol === 'anthropic' ? 'x-api-key' : 'bearer'
}

