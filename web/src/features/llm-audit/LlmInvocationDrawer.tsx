import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { LLMInvocationDetail } from '@/api/types'
import { CopyButton } from '@/components/CopyButton'
import { CopySection } from '@/components/CopySection'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { fullTime, humanDuration, humanTokens } from '@/lib/format'
import { contentToText, extractReasoning, msgColor, toMsgView } from '@/lib/llmMessage'
import { invocationStatus } from '@/lib/llmInvocationStatus'
import { isTimeout, throughputLabel } from '@/lib/llmTiming'
import { toToolCalls } from '@/lib/toolCalls'
import { ToolCallCard } from './ToolCallCard'

interface LlmInvocationDrawerProps {
  open: boolean
  detail: LLMInvocationDetail | null
  loading: boolean
  error: string
  onOpenChange: (open: boolean) => void
}

// LLM 调用详情抽屉：点审计表某行 → 右侧滑出，展示元信息 KV + 输入消息 + 返回结果原文。
// messages 是 OpenAI 风格消息数组，按条渲染（role 标签 + 正文）；结构不符时退化为 JSON 展示。
// 解析逻辑（contentToText/toMsgView/reasoning 提取）已抽到 lib/llmMessage.ts 独立测试；
// 复制状态机抽到 useCopyToClipboard；重复的「标题+复制按钮+内容」结构抽到 CopySection。
export function LlmInvocationDrawer({ open, detail, loading, error, onOpenChange }: LlmInvocationDrawerProps) {
  const { copiedKey, copy } = useCopyToClipboard()

  // messages 是「每轮完整重发的对话历史快照」——第 N 次调用的 messages 几乎完整包含第 N-1 次的。
  // 这里只取本次增量：末条消息即"这次新喂进去的东西"。完整历史可「复制完整上下文」取走。
  const rawMessages = detail?.messages
  const inputDelta = Array.isArray(rawMessages) && rawMessages.length > 0 ? toMsgView(rawMessages[rawMessages.length - 1]) : null
  const historyCount = Array.isArray(rawMessages) ? rawMessages.length : 0

  const rawResult = detail?.result
  const resultText = rawResult == null ? '' : typeof rawResult === 'string' ? rawResult : contentToText((rawResult as Record<string, unknown>).content)
  const resultCalls =
    rawResult && typeof rawResult === 'object'
      ? toToolCalls((rawResult as Record<string, unknown>).tool_calls ?? (rawResult as Record<string, unknown>).function_call)
      : []
  // 「返回结果」节的复制：只复制本节展示的东西（正文 + 工具调用）。
  const resultCopyText = [resultText, ...resultCalls.map((tc) => (tc.args ? `${tc.name}(${tc.args})` : `${tc.name}()`))]
    .filter(Boolean)
    .join('\n\n')

  const reasoning = extractReasoning(rawResult)

  // 兜底：result 既无 content 也无 tool_calls 时，整体 JSON 展示。
  const resultRaw = !resultText && resultCalls.length === 0 && rawResult != null ? JSON.stringify(rawResult, null, 2) : ''
  const messagesRaw = !inputDelta && rawMessages != null ? JSON.stringify(rawMessages, null, 2) : ''

  const { accent: roleColor } = agentAccent(detail?.role)
  const status = detail ? invocationStatus(detail) : null

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-50 flex h-full w-full flex-col bg-surface shadow-2xl focus:outline-none sm:max-w-[620px]">
          <Dialog.Title className="sr-only">LLM 调用详情</Dialog.Title>
          <div
            className="border-b border-border border-l-4 px-5 pb-3.5 pt-4"
            style={{ borderLeftColor: roleColor, background: `linear-gradient(90deg, ${roleColor}14, transparent 60%)` }}
          >
            <div className="flex items-center gap-2.5">
              <span className="text-sm font-bold" style={{ color: roleColor }}>
                {detail ? agentLabel(detail.role) || detail.role || '调用详情' : '调用详情'}
              </span>
              {status ? (
                <span
                  className="rounded px-2 py-0.5 text-[11px] font-semibold"
                  style={{
                    color: `var(--sev-${status.level === 'critical' ? 'critical' : 'medium'})`,
                    background: `color-mix(in srgb, var(--sev-${status.level === 'critical' ? 'critical' : 'medium'}) 14%, transparent)`,
                  }}
                  title={status.detail}
                >
                  {status.label}
                </span>
              ) : detail ? (
                <span className="rounded bg-surface-2 px-2 py-0.5 text-[11px] font-semibold text-muted">{detail.finish_reason || '完成'}</span>
              ) : null}
              <Dialog.Close className="ml-auto text-muted hover:text-text" aria-label="关闭">
                <X className="h-4 w-4" aria-hidden="true" />
              </Dialog.Close>
            </div>
            {detail && (
              <div className="mt-2 font-mono text-[12.5px] text-text">
                {detail.model}
                <span className="text-muted"> · {detail.provider}</span>
              </div>
            )}
          </div>

          <div className="flex flex-1 min-h-0 flex-col gap-5.5 overflow-y-auto px-5 py-4.5">
            {loading ? (
              <div className="py-10 text-center text-[13px] text-muted">加载中…</div>
            ) : error ? (
              <div className="py-10 text-center text-[13px] text-sev-critical">⚠ {error}</div>
            ) : detail ? (
              <>
                {/* 元信息：语义化定义列表，屏幕阅读器能理解字段-值对应关系。 */}
                <section>
                  <h3 className="mb-2.5 text-[13px] font-semibold text-text">元信息</h3>
                  <dl className="grid grid-cols-[84px_1fr] items-baseline gap-x-3 gap-y-1.5">
                    <dt className="text-xs text-muted">request_id</dt>
                    <dd className="flex items-center gap-2 break-all font-mono text-[12.5px] text-text">
                      {detail.request_id}
                      <CopyButton compact copied={copiedKey === 'rid'} onClick={() => void copy('rid', detail.request_id)} />
                    </dd>
                    <dt className="text-xs text-muted">时间</dt>
                    <dd className="font-mono text-[12.5px] text-text">{fullTime(detail.created_at)}</dd>
                    <dt className="text-xs text-muted">Tokens</dt>
                    <dd className="font-mono text-[12.5px] text-text">
                      输入 {humanTokens(detail.in_tokens)} · 输出 {humanTokens(detail.out_tokens)}
                      {detail.cached_tokens > 0 && ` · 缓存 ${humanTokens(detail.cached_tokens)}`}
                    </dd>
                    <dt className="text-xs text-muted">响应耗时</dt>
                    <dd className="font-mono text-[12.5px] text-text">
                      {detail.is_stream && detail.ttft_ms > 0 && `首字 ${humanDuration(detail.ttft_ms)} · `}
                      总时长 {humanDuration(detail.latency_ms)}
                      {isTimeout(detail) ? (
                        <span className="font-semibold text-sev-critical"> · 看门狗超时</span>
                      ) : (
                        throughputLabel(detail) && ` · ${throughputLabel(detail)}`
                      )}
                    </dd>
                    <dt className="text-xs text-muted">结束原因</dt>
                    <dd className="font-mono text-[12.5px] text-text">{detail.finish_reason || '—'}</dd>
                    <dt className="text-xs text-muted">传输</dt>
                    <dd className="text-[12.5px] text-text">{detail.is_stream ? '流式' : '非流式'}</dd>
                    {detail.agent_id && (
                      <>
                        <dt className="text-xs text-muted">agent</dt>
                        <dd className="font-mono text-[12.5px] text-text">{detail.agent_id}</dd>
                      </>
                    )}
                  </dl>
                </section>

                {/* 错误单独成节，最先看到 */}
                {detail.error_message && (
                  <section>
                    <h3 className="mb-2.5 text-[13px] font-semibold text-text">错误</h3>
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-sev-critical" style={{ borderColor: 'color-mix(in srgb, var(--sev-critical) 40%, var(--border))' }}>
                      {detail.error_message}
                    </pre>
                  </section>
                )}

                {/* 本次输入增量 */}
                <CopySection
                  title="本次输入"
                  hint={historyCount > 1 ? `增量（上下文共 ${historyCount} 条）` : undefined}
                  copyText={rawMessages != null ? JSON.stringify(rawMessages, null, 2) : undefined}
                  copied={copiedKey === 'msgs'}
                  onCopy={() => void copy('msgs', JSON.stringify(detail.messages, null, 2))}
                >
                  {inputDelta && (
                    <div className="border-l-[3px] pl-2.5" style={{ borderColor: msgColor(inputDelta.role) }}>
                      <div className="mb-1">
                        <span className="text-[11px] font-bold uppercase tracking-wide" style={{ color: msgColor(inputDelta.role) }}>
                          {inputDelta.role}
                        </span>
                      </div>
                      {inputDelta.text && (
                        <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-text">
                          {inputDelta.text}
                        </pre>
                      )}
                      {inputDelta.calls.map((tc) => (
                        <ToolCallCard key={tc.id} call={tc} />
                      ))}
                      {!inputDelta.text && inputDelta.calls.length === 0 && <div className="text-[12.5px] text-muted opacity-70">(空)</div>}
                    </div>
                  )}
                  {messagesRaw && (
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-text">
                      {messagesRaw}
                    </pre>
                  )}
                  {!inputDelta && !messagesRaw && <div className="text-[12.5px] text-muted opacity-70">无输入消息</div>}
                </CopySection>

                {/* 模型思考过程 */}
                {reasoning && (
                  <CopySection
                    title="思考过程"
                    hint="推理链"
                    copyText={reasoning}
                    copied={copiedKey === 'rsn'}
                    onCopy={() => void copy('rsn', reasoning)}
                  >
                    <pre
                      className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border-l-[3px] bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-muted"
                      style={{ borderLeftColor: 'color-mix(in srgb, var(--accent) 30%, var(--border))' }}
                    >
                      {reasoning}
                    </pre>
                  </CopySection>
                )}

                {/* 返回结果 */}
                <CopySection
                  title="返回结果"
                  copyText={resultCopyText}
                  copied={copiedKey === 'res'}
                  onCopy={() => void copy('res', resultCopyText)}
                >
                  {resultText && (
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-text">
                      {resultText}
                    </pre>
                  )}
                  {resultCalls.length > 0 && (
                    <>
                      <p className="my-2 text-[11.5px] text-muted">工具调用 · {resultCalls.length}</p>
                      {resultCalls.map((tc) => (
                        <ToolCallCard key={tc.id} call={tc} />
                      ))}
                    </>
                  )}
                  {resultRaw && (
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-text">
                      {resultRaw}
                    </pre>
                  )}
                  {!resultText && resultCalls.length === 0 && !resultRaw && (
                    <div className="text-[12.5px] text-muted opacity-70">无返回内容</div>
                  )}

                  {/* 原始响应：取证用，含思考过程/extra/response_meta 等完整字段 */}
                  {detail.result != null && (
                    <div className="mt-3 border-t border-dashed border-border pt-2.5">
                      <CopyButton
                        copied={copiedKey === 'raw'}
                        onClick={() => void copy('raw', JSON.stringify(detail.result, null, 2))}
                        label="复制原始响应 JSON（完整字段）"
                        className="text-[10.5px] opacity-85"
                      />
                    </div>
                  )}
                </CopySection>
              </>
            ) : null}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
