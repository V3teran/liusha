import type { ProviderConfig, ProviderProbeTarget, ProviderType, ReasoningEffort } from '@/api/types'

// provider 配置的纯规则：与后端同名规则一一对应（llmconfig.NormalizeBaseURL /
// llmconfig.ProviderParams.Validate / framework/llm.OperationURL），前端用于实时预览
// 「实际请求」与就地校验。只认协议，不认任何具体服务。

/** 用户常把完整端点粘进 Base URL 时带上的操作路径——由 SDK 自行拼接，不属于 Base URL。 */
const OPERATION_SUFFIXES = ['/chat/completions', '/completions', '/responses', '/messages', '/models']

/**
 * 按协议规则归一 Base URL：去首尾空白与末尾斜杠、去掉误粘的操作路径；Anthropic Messages 协议
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
  if (protocol === 'anthropic_messages' && path.toLowerCase().endsWith('/v1')) path = path.slice(0, -3)
  path = path.replace(/\/+$/, '')
  return `${u.protocol}//${u.host}${path}${u.search}`
}

/** 协议对话操作的实际请求地址（与 SDK 拼接规则一致）。 */
export function chatRequestUrl(protocol: ProviderType, baseUrl: string): string {
  const base = baseUrl.replace(/\/+$/, '')
  if (protocol === 'anthropic_messages') return `${base}/v1/messages`
  if (protocol === 'openai_responses') return `${base}/responses`
  return `${base}/chat/completions`
}

export { outputLimitParamOf } from '@/api/models'

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

// 上下文窗口与单次最大输出的缺省，与后端唯一的出处（llmconfig.DefaultContextWindow /
// DefaultMaxTokens）同值：取当前旗舰模型的官方上限（上下文 1M、单次输出 128K）。它们只是上限，
// 不多花钱；上限低于缺省的模型，上游拒绝输出上限时运行期自动下调。
export const DEFAULT_CONTEXT_WINDOW = 1048576
export const DEFAULT_MAX_TOKENS = 131072

// 思考深度的档位表与展示名（与后端唯一的出处 framework/llm.ReasoningEfforts 同序）：由低到高四档。
// 空串不是一档，是「没设置」——下拉框以「按模型默认」表示它。
export const REASONING_EFFORT_OPTIONS: { value: ReasoningEffort; label: string }[] = [
  { value: 'low', label: '低' },
  { value: 'medium', label: '中' },
  { value: 'high', label: '高' },
  { value: 'max', label: '最高' },
]

/** 思考深度的展示名；没设置时说明按模型默认。 */
export function reasoningEffortLabel(effort: ReasoningEffort | ''): string {
  return REASONING_EFFORT_OPTIONS.find((o) => o.value === effort)?.label ?? '按模型默认'
}

/**
 * 保存前置：协议已选、base_url/default_model 必填且格式合法、context_window > 0、
 * 新建必须带密钥；编辑留空 = 沿用已存密钥。
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
    !(typeof cw === 'number' && cw > 0)
  )
}

/**
 * 新建 provider 的空白初值：协议不预设（必须由用户选择），能力取保守值；上下文窗口与单次
 * 最大输出取缺省（与后端同值）。此前预填的是 65536 / 4096——远低于旗舰模型的上限，推理模型
 * 的思考计入输出，思考加正文轻易超过 4096，实测一个 provider 上 19% 的规划输出被截断。
 */
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
    max_tokens: DEFAULT_MAX_TOKENS,
    reasoning_effort: '',
    supports_tools: true,
    supports_vision: false,
    context_window: DEFAULT_CONTEXT_WINDOW,
    description: '',
    sort_order: 0,
    enabled: true,
  }
}

/** 协议的标准鉴权方式（选协议时作为初值，测试连接可改写）。 */
export function defaultAuthScheme(protocol: ProviderType): ProviderConfig['auth_scheme'] {
  return protocol === 'anthropic_messages' ? 'x-api-key' : 'bearer'
}


/**
 * 实测（测试连接、拉取模型列表）的共同前置：缺了就发不出请求。点击时就地给出原因，
 * 不禁用按钮静默拦截。
 */
export function probeTargetMissing(d: ProviderConfig): string | undefined {
  if (!d.type) return '请先选择协议'
  if (!d.base_url.trim() || baseUrlError(d.base_url)) return '请先填写完整的 Base URL'
  if (!d.api_key && !d.key_present) return '请先填写 API Key'
  return undefined
}

/** 表单 → 实测目标（调用方已过 probeTargetMissing：协议必已选）。api_key 留空 = 用已存密钥。 */
export function probeTarget(d: ProviderConfig): ProviderProbeTarget {
  return {
    code: d.code || undefined,
    type: d.type as ProviderType,
    base_url: d.base_url,
    api_key: d.api_key || undefined,
    auth_scheme: d.auth_scheme,
  }
}

/**
 * 连接标识：协议、归一后的地址与密钥来源。它变了，此前的实测结果（拉到的模型列表、测试
 * 连接的结论）就不再属于当前连接。鉴权方式不在其中：它只决定协商的先后，连到的还是同一个服务。
 */
export function connectionKey(d: ProviderConfig): string {
  return [d.type, normalizeBaseUrl(d.type, d.base_url), d.code, d.api_key ?? ''].join('\u0000')
}
