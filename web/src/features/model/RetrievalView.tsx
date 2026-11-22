import { useState } from 'react'
import { errorText } from '@/api/client'
import type { RetrievalDiagnosis } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { confirmAction } from '@/components/ui/confirm'
import { toast } from '@/components/ui/toast'
import { ErrorState } from '@/components/ui/ErrorState'
import { InlineError } from '@/components/ui/InlineError'
import { LoadingState } from '@/components/ui/LoadingState'
import {
  useRetrievalModel,
  useSaveRetrievalModel,
  useClearRetrievalModel,
  useDiagnoseRetrievalModel,
} from './useModelQueries'

/** 检索模型诊断步骤的展示名（key 与后端 DiagnosisStep 对应）。 */
const STEP_LABEL: Record<string, string> = { embedding: '向量', rerank: '重排' }

// 检索模型分区：知识库语义检索（向量 + 重排）的密钥接入。模型名与地址固定只读——向量维度
// 绑定库结构、检索门槛按模型标定，换模型须重建全部向量，故只开放密钥。
// 未配置时知识库自动字面检索、不阻塞；保存成功后后端立即补齐无向量条目（即时生效），
// runner 周期巡检兜底。清除需确认：密钥不再回显，只能重新填写（向量保留）。
export function RetrievalView() {
  const { data: model, isLoading, isError, error, refetch } = useRetrievalModel()
  const saveMutation = useSaveRetrievalModel()
  const clearMutation = useClearRetrievalModel()
  const diagnoseMutation = useDiagnoseRetrievalModel()

  const [draft, setDraft] = useState('')
  const [rotating, setRotating] = useState(false) // 已配置时点「更换密钥」才展开输入，防误清空
  const [actionError, setActionError] = useState('')
  const [diagnosis, setDiagnosis] = useState<RetrievalDiagnosis | null>(null)

  const configured = model?.key_present ?? false
  // 输入框可见：未配置，或已配置且正在更换密钥
  const showInput = !configured || rotating

  const onSave = () => {
    const key = draft.trim()
    if (!key) return
    setActionError('')
    saveMutation.mutate(key, {
      onSuccess: () => {
        setDraft('')
        setRotating(false)
        setDiagnosis(null)
        toast.success('检索模型密钥已保存，无向量条目正在补齐')
      },
      onError: (e) => setActionError(errorText(e, '保存失败')),
    })
  }

  const onTest = () => {
    setActionError('')
    // 输入框有值测表单当前值（不落库）；否则测已存密钥
    diagnoseMutation.mutate(showInput && draft.trim() ? draft.trim() : undefined, {
      onSuccess: (res) => setDiagnosis(res),
      onError: (e) => setActionError(errorText(e, '测试失败')),
    })
  }

  const onClear = async () => {
    const ok = await confirmAction({
      title: '清除检索模型密钥？',
      message: '知识库将回到字面检索。已有向量保留，重新填写密钥后继续使用。',
      confirmLabel: '确认清除',
    })
    if (!ok) return
    setActionError('')
    clearMutation.mutate(undefined, {
      onSuccess: () => {
        setDraft('')
        setRotating(false)
        setDiagnosis(null)
        toast.success('检索模型密钥已清除，知识库回到字面检索')
      },
      onError: (e) => setActionError(errorText(e, '清除失败')),
    })
  }

  const busy = saveMutation.isPending || clearMutation.isPending || diagnoseMutation.isPending
  const saveDisabled = showInput && draft.trim() === ''

  return (
    <section aria-label="检索模型">
      <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-text">检索模型</h2>
        </div>
        {isLoading ? (
          <LoadingState className="py-10" />
        ) : isError ? (
          <ErrorState message={error instanceof Error ? error.message : undefined} onRetry={() => void refetch()} />
        ) : (
          <div className="flex flex-col divide-y divide-border">
            {/* 模型名只读展示（固定不可换） */}
            <div className="flex flex-col gap-1.5 px-4 py-3 text-sm">
              <div className="flex items-baseline justify-between gap-4">
                <span className="text-text-secondary">向量模型</span>
                <code className="font-mono text-xs text-text">{model!.embedding_model}</code>
              </div>
              <div className="flex items-baseline justify-between gap-4">
                <span className="text-text-secondary">重排模型</span>
                <code className="font-mono text-xs text-text">{model!.rerank_model}</code>
              </div>
            </div>

            {/* 密钥：写-only 凭据，落库后永不回显明文 */}
            <div className="flex flex-col gap-3 px-4 py-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div className="min-w-0 text-sm">
                  <span className="text-text-secondary">密钥</span>
                  {configured && !rotating && (
                    <span className="ml-3 font-mono text-xs text-text">
                      {`已配置 ····${model!.key_last4}`}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <Button size="sm" variant="ghost" disabled={busy} onClick={onTest}>
                    测试
                  </Button>
                  {configured && !rotating ? (
                    <>
                      <Button size="sm" variant="ghost" disabled={busy} onClick={() => setRotating(true)}>
                        更换密钥
                      </Button>
                      <Button size="sm" variant="ghost" disabled={busy} onClick={() => void onClear()}>
                        清除
                      </Button>
                    </>
                  ) : (
                    <>
                      {configured && (
                        <Button
                          size="sm"
                          variant="ghost"
                          disabled={busy}
                          onClick={() => {
                            setRotating(false)
                            setDraft('')
                            setActionError('')
                          }}
                        >
                          取消
                        </Button>
                      )}
                      <Button size="sm" disabled={busy || saveDisabled} onClick={onSave}>
                        保存
                      </Button>
                    </>
                  )}
                </div>
              </div>
              {showInput && (
                <input
                  type="password"
                  value={draft}
                  onChange={(e) => setDraft(e.target.value)}
                  placeholder="粘贴 API Key（同时启用向量与重排）"
                  aria-label="检索模型 API Key"
                  autoComplete="off"
                  spellCheck={false}
                  className="w-full rounded-md border border-border bg-surface-2 px-3 py-2 font-mono text-sm text-text placeholder:text-text-tertiary focus:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                />
              )}
              {!configured && !rotating && (
                <p className="text-xs text-text-tertiary">
                  未配置：知识库只做字面检索。填写密钥后自动启用语义检索，立即生效。
                </p>
              )}
              {configured && !rotating && model!.updated_at && (
                <p className="text-xs text-text-tertiary">
                  更新于 {new Date(model!.updated_at).toLocaleString()}
                </p>
              )}
            </div>
          </div>
        )}

        {/* 诊断结果：两步就地展示，成功带耗时、失败带上游原因 */}
        {diagnosis && (
          <div className="flex flex-col gap-1.5 border-t border-border px-4 py-3 text-sm">
            {diagnosis.steps.map((s) => (
              <div key={s.key} className="flex items-baseline justify-between gap-4">
                <span className="text-text-secondary">{STEP_LABEL[s.key] ?? s.key}</span>
                <span className={s.status === 'ok' ? 'text-status-success' : 'text-status-error'}>
                  {s.status === 'ok' ? `通过 · ${s.latency_ms ?? 0}ms` : s.detail}
                </span>
              </div>
            ))}
          </div>
        )}
        {actionError && (
          <div className="border-t border-border px-4 py-3">
            <InlineError>{actionError}</InlineError>
          </div>
        )}
      </div>
    </section>
  )
}
