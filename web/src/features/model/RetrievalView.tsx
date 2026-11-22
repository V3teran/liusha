import { useEffect, useMemo, useState } from 'react'
import { errorText } from '@/api/client'
import type { RetrievalDiagnosis, RetrievalModelDraft } from '@/api/types'
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

// 检索模型分区：与「对话模型」同构的配置表单——服务地址、向量模型、重排模型、一把密钥。
// 空字段 = 出厂默认（占位提示展示默认值）。换模型的硬约束写进提示与实测：
// 向量维度须与库结构一致（1024，测试实测校验）；更换向量模型会作废全部旧向量并立即重建。
// 未配置密钥时知识库自动字面检索、不阻塞；清除只清密钥，保留地址与模型名的自定义。
export function RetrievalView() {
  const { data: model, isLoading, isError, error, refetch } = useRetrievalModel()
  const saveMutation = useSaveRetrievalModel()
  const clearMutation = useClearRetrievalModel()
  const diagnoseMutation = useDiagnoseRetrievalModel()

  const [draft, setDraft] = useState({ base_url: '', embedding_model: '', rerank_model: '' })
  const [keyDraft, setKeyDraft] = useState('')
  const [rotating, setRotating] = useState(false) // 已配置时点「更换密钥」才展开密钥输入，防误清空
  const [actionError, setActionError] = useState('')
  const [diagnosis, setDiagnosis] = useState<RetrievalDiagnosis | null>(null)

  // 生效配置到达后作为表单初值（已折算默认值，所见即可保存）
  useEffect(() => {
    if (model) setDraft({ base_url: model.base_url, embedding_model: model.embedding_model, rerank_model: model.rerank_model })
  }, [model])

  const configured = model?.key_present ?? false
  const showKeyInput = !configured || rotating

  const draftBody = useMemo<RetrievalModelDraft & { api_key?: string }>(
    () => ({ ...draft, api_key: showKeyInput && keyDraft.trim() ? keyDraft.trim() : undefined }),
    [draft, keyDraft, showKeyInput],
  )

  const busy = saveMutation.isPending || clearMutation.isPending || diagnoseMutation.isPending

  const onSave = () => {
    setActionError('')
    saveMutation.mutate(draftBody, {
      onSuccess: () => {
        setKeyDraft('')
        setRotating(false)
        setDiagnosis(null)
        toast.success('检索模型已保存，向量重建进行中')
      },
      onError: (e) => setActionError(errorText(e, '保存失败')),
    })
  }

  const onTest = () => {
    setActionError('')
    diagnoseMutation.mutate(draftBody, {
      onSuccess: (res) => setDiagnosis(res),
      onError: (e) => setActionError(errorText(e, '测试失败')),
    })
  }

  const onClearKey = async () => {
    const ok = await confirmAction({
      title: '清除检索模型密钥？',
      message: '知识库将回到字面检索；地址与模型名的自定义保留，重新填写密钥即恢复。',
      confirmLabel: '确认清除',
    })
    if (!ok) return
    setActionError('')
    clearMutation.mutate(undefined, {
      onSuccess: () => {
        setKeyDraft('')
        setRotating(false)
        setDiagnosis(null)
        toast.success('检索模型密钥已清除，知识库回到字面检索')
      },
      onError: (e) => setActionError(errorText(e, '清除失败')),
    })
  }

  const inputCls =
    'w-full rounded-md border border-border bg-surface-2 px-3 py-2 font-mono text-sm text-text placeholder:text-text-tertiary focus:outline-none focus-visible:ring-2 focus-visible:ring-accent'

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
            {/* 服务地址与模型名：与「对话模型」表单同密度；空值保存即回出厂默认 */}
            <div className="flex flex-col gap-3 px-4 py-3">
              <label className="flex flex-col gap-1.5 text-sm">
                <span className="text-text-secondary">服务地址</span>
                <input
                  value={draft.base_url}
                  onChange={(e) => setDraft((d) => ({ ...d, base_url: e.target.value }))}
                  placeholder="https://api.jina.ai（留空 = 出厂默认）"
                  aria-label="检索服务地址"
                  autoComplete="off"
                  spellCheck={false}
                  className={inputCls}
                />
              </label>
              <label className="flex flex-col gap-1.5 text-sm">
                <span className="text-text-secondary">向量模型</span>
                <input
                  value={draft.embedding_model}
                  onChange={(e) => setDraft((d) => ({ ...d, embedding_model: e.target.value }))}
                  placeholder="jina-embeddings-v5-text-small（留空 = 出厂默认）"
                  aria-label="向量模型名"
                  autoComplete="off"
                  spellCheck={false}
                  className={inputCls}
                />
              </label>
              <label className="flex flex-col gap-1.5 text-sm">
                <span className="text-text-secondary">重排模型</span>
                <input
                  value={draft.rerank_model}
                  onChange={(e) => setDraft((d) => ({ ...d, rerank_model: e.target.value }))}
                  placeholder="jina-reranker-v2-base-multilingual（留空 = 出厂默认）"
                  aria-label="重排模型名"
                  autoComplete="off"
                  spellCheck={false}
                  className={inputCls}
                />
              </label>
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
                {configured && !rotating && (
                  <Button size="sm" variant="ghost" disabled={busy} onClick={() => setRotating(true)}>
                    更换密钥
                  </Button>
                )}
              </div>
              {showKeyInput && (
                <input
                  type="password"
                  value={keyDraft}
                  onChange={(e) => setKeyDraft(e.target.value)}
                  placeholder="粘贴 API Key（同时启用向量与重排）"
                  aria-label="检索模型 API Key"
                  autoComplete="off"
                  spellCheck={false}
                  className={inputCls}
                />
              )}
              {!configured && (
                <p className="text-xs text-text-tertiary">
                  未配置密钥：知识库只做字面检索。填写后自动启用语义检索，立即生效。
                </p>
              )}
              <p className="text-xs text-text-tertiary">
                向量维度须与库结构一致（1024 维，「测试」会实测）；更换向量模型会作废全部旧向量并立即重建。
              </p>
            </div>
          </div>
        )}

        {/* 操作行：测试按表单当前值实测（不落库），保存整行落库 */}
        <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
          <Button size="sm" variant="ghost" disabled={busy} onClick={onTest}>
            测试
          </Button>
          {configured && !rotating && (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => void onClearKey()}>
              清除密钥
            </Button>
          )}
          <Button size="sm" disabled={busy} onClick={onSave}>
            保存
          </Button>
        </div>

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
