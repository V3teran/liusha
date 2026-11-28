import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, CircleDashed, Loader2, PlugZap, XCircle } from 'lucide-react'
import { errorText } from '@/api/client'
import type {
  Diagnosis,
  DiagnosisStep,
  DiagnosisStepKey,
  DiagnosisStepStatus,
  ProviderConfig,
} from '@/api/types'
import { Button } from '@/components/ui/Button'
import { InlineError } from '@/components/ui/InlineError'
import { cn } from '@/lib/utils'
import { TYPE_LABEL } from './providerForm'
import { connectionKey, probeTarget, probeTargetMissing } from './providerRules'
import { useDiagnoseProvider } from './useModelQueries'

// 检查项标签是中性名词：结论由状态图标与详情表达（断言式标签配上失败结果会自相矛盾）。
const STEP_LABEL: Record<DiagnosisStepKey, string> = {
  endpoint: '端点',
  auth: '鉴权',
  model: '模型',
  tools: '工具调用',
  embedding: '向量',
  rerank: '重排',
}

const STATUS_STYLE: Record<DiagnosisStepStatus, { icon: typeof CheckCircle2; className: string }> = {
  ok: { icon: CheckCircle2, className: 'text-accent' },
  fail: { icon: XCircle, className: 'text-status-error' },
  warn: { icon: AlertTriangle, className: 'text-status-warning' },
  skip: { icon: CircleDashed, className: 'text-text-tertiary' },
}

/** 测试结论所对应的配置：连接与模型变了，已有结论就不再代表当前配置。 */
function inputFingerprint(d: ProviderConfig): string {
  return [connectionKey(d), d.default_model.trim()].join('\u0000')
}

/** 测试请求所依据的全部输入：任一项在请求在途期间变化，结果就不能回填。 */
function requestFingerprint(d: ProviderConfig): string {
  return [inputFingerprint(d), d.auth_scheme, d.output_limit_param, d.reasoning_effort, String(d.key_present)].join('\u0000')
}

/** 测试连接的前置：实测目标齐全，且填了模型——可用性只能由对话请求证明。 */
function diagnoseMissing(d: ProviderConfig): string | undefined {
  return probeTargetMissing(d) ?? (d.default_model.trim() ? undefined : '请先填写默认模型（可从模型列表拉取）')
}

/** 测试得出的、可直接回填表单的请求画像（不含需用户确认的建议）。 */
export function profilePatch(diag: Diagnosis): Partial<ProviderConfig> {
  if (!diag.profile) return {}
  const patch: Partial<ProviderConfig> = {
    auth_scheme: diag.profile.auth_scheme,
    output_limit_param: diag.profile.output_limit_param,
  }
  if (diag.profile.supports_tools !== undefined) patch.supports_tools = diag.profile.supports_tools
  return patch
}

// 测试连接：以默认模型按表单当前值实测端点 → 鉴权 → 模型 → 工具调用（按返回顺序渲染，即
// 依赖顺序），逐项展示结果与实际请求地址。打通时自动回填请求画像（鉴权方式、输出上限字段、
// 工具调用）；「换个写法能通」时给出建议，用户一键采纳（建议改写的是用户填的协议/地址，必须确认）。
export function DiagnosisPanel({
  draft,
  onApply,
}: {
  draft: ProviderConfig
  onApply: (patch: Partial<ProviderConfig>) => void
}) {
  const diagnose = useDiagnoseProvider()
  const [result, setResult] = useState<Diagnosis | null>(null)
  const [error, setError] = useState('')
  const [ranFor, setRanFor] = useState('')
  // 最新草稿：响应到达时据此判断它是否仍对应当前配置。
  const latest = useRef(draft)
  useEffect(() => {
    latest.current = draft
  }, [draft])

  const run = () => {
    const missing = diagnoseMissing(draft)
    setResult(null)
    setError(missing ?? '')
    if (missing) return
    const sent = requestFingerprint(draft)
    diagnose.mutate(
      {
        ...probeTarget(draft),
        default_model: draft.default_model.trim(),
        output_limit_param: draft.output_limit_param,
        reasoning_effort: draft.reasoning_effort,
      },
      {
        onSuccess: (diag) => {
          setResult(diag)
          if (requestFingerprint(latest.current) !== sent) {
            // 在途期间配置被改过：只作为过期结果展示，不回填。
            setRanFor(inputFingerprint(draft))
            return
          }
          // 无建议时画像对应的就是当前配置：直接回填（连同归一后的地址，所见即所存）。
          const patch: Partial<ProviderConfig> =
            !diag.suggestion && diag.profile ? { base_url: diag.base_url, ...profilePatch(diag) } : {}
          if (Object.keys(patch).length > 0) onApply(patch)
          setRanFor(inputFingerprint({ ...draft, ...patch }))
        },
        onError: (e) => setError(errorText(e, '测试连接失败')),
      },
    )
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
  const running = diagnose.isPending

  return (
    <section aria-label="测试连接" className="flex flex-col gap-3">
      <div>
        <Button variant="secondary" size="sm" onClick={run} disabled={running}>
          {running ? <Loader2 className="animate-spin" /> : <PlugZap />}
          {running ? '测试中…' : '测试连接'}
        </Button>
      </div>

      {error && <InlineError>{error}</InlineError>}

      {result && (
        <div className={cn('flex flex-col gap-2 rounded-lg border border-border bg-surface px-4 py-3', stale && 'opacity-60')}>
          <p className={cn('text-xs font-medium', result.ok ? 'text-accent' : 'text-status-error')}>
            {result.ok ? '配置可用' : '未通过'}
            {stale && <span className="ml-2 font-normal text-text-tertiary">配置已改动，请重新测试</span>}
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
              <Button size="xs" onClick={adopt} disabled={stale}>
                采纳
              </Button>
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
          <span className="block break-all font-mono text-2xs text-text-tertiary">
            {step.request}
            {step.latency_ms ? ` · ${step.latency_ms}ms` : ''}
          </span>
        )}
      </div>
    </li>
  )
}
