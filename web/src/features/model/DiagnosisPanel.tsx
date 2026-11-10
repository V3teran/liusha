import { useState } from 'react'
import { AlertTriangle, CheckCircle2, CircleDashed, Loader2, Stethoscope, XCircle } from 'lucide-react'
import { errorText } from '@/api/client'
import { diagnoseProvider } from '@/api/models'
import type {
  Diagnosis,
  DiagnosisStep,
  DiagnosisStepKey,
  DiagnosisStepStatus,
  ProviderConfig,
  ProviderType,
} from '@/api/types'
import { cn } from '@/lib/utils'
import { TYPE_LABEL } from './providerForm'
import { baseUrlError, normalizeBaseUrl } from './providerRules'

// 检查项标签是中性名词：结论由状态图标与详情表达（断言式标签配上失败结果会自相矛盾）。
const STEP_LABEL: Record<DiagnosisStepKey, string> = {
  endpoint: '端点',
  auth: '鉴权',
  models: '模型列表',
  model: '模型',
  tools: '工具调用',
}

const STATUS_STYLE: Record<DiagnosisStepStatus, { icon: typeof CheckCircle2; className: string }> = {
  ok: { icon: CheckCircle2, className: 'text-accent' },
  fail: { icon: XCircle, className: 'text-sev-critical' },
  warn: { icon: AlertTriangle, className: 'text-sev-high' },
  skip: { icon: CircleDashed, className: 'text-text-tertiary' },
}

/** 诊断输入的指纹：这些字段变了，已有结果就不再代表当前配置。 */
function inputFingerprint(d: ProviderConfig): string {
  const type = d.type || 'openai_compat'
  return [d.type, normalizeBaseUrl(type, d.base_url), d.default_model.trim(), d.api_key ?? ''].join('\u0000')
}

/** 诊断得出的、可直接回填表单的请求画像（不含需用户确认的建议），连同实测可用的自动选用模型。 */
export function profilePatch(diag: Diagnosis): Partial<ProviderConfig> {
  const patch: Partial<ProviderConfig> = {}
  if (!diag.profile) return patch
  if (diag.ok && diag.auto_model) patch.default_model = diag.auto_model
  patch.auth_scheme = diag.profile.auth_scheme
  if (diag.profile.output_limit_param) patch.output_limit_param = diag.profile.output_limit_param
  if (diag.profile.supports_tools !== undefined) patch.supports_tools = diag.profile.supports_tools
  return patch
}

// 连接诊断面板：按表单当前值实测端点 → 鉴权 → 模型列表 → 模型 → 工具调用（按返回顺序
// 渲染，即依赖顺序），逐项展示结果与实际请求地址。打通时自动回填请求画像（鉴权方式、输出上限字段、工具调用）；
// 「换个写法能通」时给出建议，用户一键采纳（建议改写的是用户填的协议/地址，必须确认）。
export function DiagnosisPanel({
  draft,
  onApply,
  onModels,
}: {
  draft: ProviderConfig
  onApply: (patch: Partial<ProviderConfig>) => void
  onModels: (models: string[]) => void
}) {
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<Diagnosis | null>(null)
  const [error, setError] = useState('')
  const [ranFor, setRanFor] = useState('')

  const precondition = (): string | undefined => {
    if (!draft.type) return '请先选择协议'
    if (!draft.base_url.trim() || baseUrlError(draft.base_url)) return '请先填写完整的 Base URL'
    if (!draft.api_key && !draft.key_present) return '请先填写 API Key'
    return undefined
  }

  const run = async () => {
    const missing = precondition()
    setResult(null)
    setError(missing ?? '')
    if (missing) return
    setRunning(true)
    try {
      const diag = await diagnoseProvider({
        code: draft.code || undefined,
        type: draft.type as ProviderType,
        base_url: draft.base_url,
        default_model: draft.default_model.trim() || undefined,
        api_key: draft.api_key || undefined,
        auth_scheme: draft.auth_scheme,
        output_limit_param: draft.output_limit_param,
      })
      setResult(diag)
      onModels(diag.models)
      // 无建议时画像对应的就是当前配置：直接回填（连同归一后的地址与自动选用的模型，所见即所存）。
      const patch: Partial<ProviderConfig> =
        !diag.suggestion && diag.profile ? { base_url: diag.base_url, ...profilePatch(diag) } : {}
      if (Object.keys(patch).length > 0) onApply(patch)
      setRanFor(inputFingerprint({ ...draft, ...patch }))
    } catch (e) {
      setError(errorText(e, '诊断请求失败'))
    } finally {
      setRunning(false)
    }
  }

  const adopt = () => {
    if (!result?.suggestion) return
    const s = result.suggestion
    const patch: Partial<ProviderConfig> = { type: s.type, base_url: s.base_url, auth_scheme: s.auth_scheme, ...profilePatch(result) }
    onApply(patch)
    setResult({ ...result, suggestion: undefined })
    setRanFor(inputFingerprint({ ...draft, ...patch }))
  }

  const stale = result !== null && ranFor !== inputFingerprint(draft)

  return (
    <section aria-label="连接诊断" className="rounded-lg border border-border bg-surface px-4 py-3">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <h4 className="text-sm font-medium text-text">连接诊断</h4>
          <p className="mt-0.5 text-xs text-text-tertiary">实测端点、鉴权、模型列表、模型与工具调用，自动识别鉴权方式等细节</p>
        </div>
        <button
          type="button"
          onClick={() => void run()}
          disabled={running}
          className="flex flex-shrink-0 items-center gap-1.5 rounded-lg border border-border px-3.5 py-1.5 text-[13px] text-text-secondary transition-colors hover:border-accent hover:text-accent disabled:cursor-default disabled:opacity-40"
        >
          {running ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Stethoscope className="h-3.5 w-3.5" />}
          {running ? '诊断中…' : '运行诊断'}
        </button>
      </div>

      {error && (
        <p role="alert" className="mt-3 text-xs text-sev-critical">
          {error}
        </p>
      )}

      {result && (
        <div className={cn('mt-3 flex flex-col gap-2', stale && 'opacity-60')}>
          <p className={cn('text-xs font-medium', result.ok ? 'text-accent' : 'text-sev-critical')}>
            {result.ok ? '配置可用' : '未通过'}
            {stale && <span className="ml-2 font-normal text-text-tertiary">配置已改动，请重新诊断</span>}
          </p>
          <ol className="flex flex-col gap-1.5">
            {result.steps.map((step) => (
              <StepRow key={step.key} step={step} />
            ))}
          </ol>
          {result.suggestion && (
            <div className="mt-1 flex items-start gap-3 rounded-lg border border-accent/40 bg-accent/5 px-3 py-2.5">
              <div className="min-w-0 flex-1 text-xs text-text">
                <p className="font-medium">建议：{result.suggestion.reason}</p>
                <p className="mt-0.5 break-all text-text-secondary">
                  协议 {TYPE_LABEL[result.suggestion.type]} · Base URL{' '}
                  <code className="font-mono">{result.suggestion.base_url}</code>
                </p>
              </div>
              <button
                type="button"
                onClick={adopt}
                className="flex-shrink-0 rounded-md border border-accent/60 px-2.5 py-1 text-xs text-accent transition-colors hover:bg-accent/10"
              >
                采纳
              </button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}

function StepRow({ step }: { step: DiagnosisStep }) {
  const { icon: Icon, className } = STATUS_STYLE[step.status]
  return (
    <li className="flex items-start gap-2 text-xs" data-step={step.key} data-status={step.status}>
      <Icon className={cn('mt-0.5 h-3.5 w-3.5 flex-shrink-0', className)} aria-label={step.status} />
      <div className="min-w-0 flex-1">
        <span className="font-medium text-text">{STEP_LABEL[step.key]}</span>
        {step.detail && <span className="ml-2 text-text-secondary">{step.detail}</span>}
        {step.request && (
          <span className="block break-all font-mono text-[11px] text-text-tertiary">
            {step.request}
            {step.latency_ms ? ` · ${step.latency_ms}ms` : ''}
          </span>
        )}
      </div>
    </li>
  )
}
